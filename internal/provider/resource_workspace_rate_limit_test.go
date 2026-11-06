package provider

import (
	"fmt"
	"maps"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const workspaceRateLimitAddress = "anthropic_workspace_rate_limit.test"

func workspaceRateLimitConfig(rateLimitID, limits string) string {
	return testProviderConfig + fmt.Sprintf(`
resource "anthropic_workspace" "test" {
  name = "Rate limited"
}

resource "anthropic_workspace_rate_limit" "test" {
  workspace_id  = anthropic_workspace.test.id
  rate_limit_id = %q
  limits = {
%s
  }
}
`, rateLimitID, limits)
}

func seededWorkspaceRateLimitConfig(workspaceID, rateLimitID, limits string) string {
	return testProviderConfig + fmt.Sprintf(`
resource "anthropic_workspace_rate_limit" "test" {
  workspace_id  = %q
  rate_limit_id = %q
  limits = {
%s
  }
}
`, workspaceID, rateLimitID, limits)
}

func checkFakeRateLimitOverrides(fake *fakeAdminAPI, want map[string]int64) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attributes := s.RootModule().Resources[workspaceRateLimitAddress].Primary.Attributes
		got := fake.rateLimitOverrides(attributes["workspace_id"], attributes["rate_limit_id"])
		if !maps.Equal(got, want) {
			return fmt.Errorf("fake overrides = %v, want %v", got, want)
		}
		return nil
	}
}

func lastRateLimitUpdateBody(fake *fakeAdminAPI) string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.rateLimitUpdateBodies) == 0 {
		return ""
	}
	return fake.rateLimitUpdateBodies[len(fake.rateLimitUpdateBodies)-1]
}

func checkLastRateLimitUpdateBodyContains(fake *fakeAdminAPI, fragments ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		body := lastRateLimitUpdateBody(fake)
		for _, fragment := range fragments {
			if !strings.Contains(body, fragment) {
				return fmt.Errorf("last update body %s does not contain %s", body, fragment)
			}
		}
		return nil
	}
}

func expectWorkspaceRateLimitAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(workspaceRateLimitAddress, action)},
	}
}

func expectEmptyWorkspaceRateLimitPlan() resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}

func workspaceRateLimitImportId(s *terraform.State) (string, error) {
	rs, ok := s.RootModule().Resources[workspaceRateLimitAddress]
	if !ok {
		return "", fmt.Errorf("resource not found: %s", workspaceRateLimitAddress)
	}
	return rs.Primary.Attributes["workspace_id"] + "/" + rs.Primary.Attributes["rate_limit_id"], nil
}

func useTestClock(t *testing.T, start time.Time) *time.Time {
	t.Helper()
	now := start
	previous := currentTime
	currentTime = func() time.Time { return now }
	t.Cleanup(func() { currentTime = previous })
	return &now
}

func TestAccWorkspaceRateLimit_Basic(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	created := workspaceRateLimitConfig(fakeHaikuRateLimitID, `
    tokens_per_day      = 5000000
    requests_per_minute = 100`)
	updated := workspaceRateLimitConfig(fakeHaikuRateLimitID, `
    tokens_per_day           = 6000000
    output_tokens_per_minute = 1000`)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: created,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair(workspaceRateLimitAddress, "workspace_id", "anthropic_workspace.test", "id"),
					resource.TestCheckResourceAttr(workspaceRateLimitAddress, "rate_limit_id", fakeHaikuRateLimitID),
					resource.TestCheckResourceAttr(workspaceRateLimitAddress, "limits.%", "2"),
					resource.TestCheckResourceAttr(workspaceRateLimitAddress, "limits.tokens_per_day", "5000000"),
					resource.TestCheckResourceAttr(workspaceRateLimitAddress, "limits.requests_per_minute", "100"),
					func(s *terraform.State) error {
						attributes := s.RootModule().Resources[workspaceRateLimitAddress].Primary.Attributes
						if want := attributes["workspace_id"] + "/" + fakeHaikuRateLimitID; attributes["id"] != want {
							return fmt.Errorf("id = %s, want %s", attributes["id"], want)
						}
						return nil
					},
					checkFakeRateLimitOverrides(fake, map[string]int64{"tokens_per_day": 5000000, "requests_per_minute": 100}),
				),
			},
			{
				Config:           created,
				ConfigPlanChecks: expectEmptyWorkspaceRateLimitPlan(),
			},
			{
				ResourceName:      workspaceRateLimitAddress,
				ImportState:       true,
				ImportStateIdFunc: workspaceRateLimitImportId,
				ImportStateVerify: true,
			},
			{
				Config:           updated,
				ConfigPlanChecks: expectWorkspaceRateLimitAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceRateLimitAddress, "limits.%", "2"),
					resource.TestCheckResourceAttr(workspaceRateLimitAddress, "limits.tokens_per_day", "6000000"),
					resource.TestCheckResourceAttr(workspaceRateLimitAddress, "limits.output_tokens_per_minute", "1000"),
					checkFakeRateLimitOverrides(fake, map[string]int64{"tokens_per_day": 6000000, "output_tokens_per_minute": 1000}),
					checkLastRateLimitUpdateBodyContains(fake, `{"value":null,"type":"requests_per_minute"}`),
				),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					for workspaceID := range fake.workspaces {
						fake.setRateLimitOverride(workspaceID, fakeHaikuRateLimitID, "input_tokens_per_minute", 200000)
					}
				},
				Config:           updated,
				ConfigPlanChecks: expectEmptyWorkspaceRateLimitPlan(),
			},
		},
		CheckDestroy: func(*terraform.State) error {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			for workspaceID := range fake.workspaces {
				got := fake.workspaceRateLimits[workspaceID][fakeHaikuRateLimitID]
				if want := map[string]int64{"input_tokens_per_minute": 200000}; !maps.Equal(got, want) {
					return fmt.Errorf("overrides after destroy = %v, want only the unmanaged %v", got, want)
				}
			}
			body := fake.rateLimitUpdateBodies[len(fake.rateLimitUpdateBodies)-1]
			for _, cleared := range []string{"output_tokens_per_minute", "tokens_per_day"} {
				if !strings.Contains(body, `{"value":null,"type":"`+cleared+`"}`) {
					return fmt.Errorf("destroy body %s does not clear %s", body, cleared)
				}
			}
			if strings.Contains(body, "input_tokens_per_minute") {
				return fmt.Errorf("destroy body %s touches the unmanaged override", body)
			}
			return nil
		},
	})
}

func TestAccWorkspaceRateLimit_ReadKeepsWritesWhileTheListLags(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)
	now := useTestClock(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	fake.mu.Lock()
	fake.rateLimitListLags = true
	fake.mu.Unlock()

	config := workspaceRateLimitConfig(fakeHaikuRateLimitID, `
    tokens_per_day = 5000000`)
	raised := workspaceRateLimitConfig(fakeHaikuRateLimitID, `
    tokens_per_day = 6000000`)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceRateLimitAddress, "limits.tokens_per_day", "5000000"),
					checkFakeRateLimitOverrides(fake, map[string]int64{"tokens_per_day": 5000000}),
				),
			},
			{
				PreConfig:        func() { *now = now.Add(30 * time.Minute) },
				Config:           config,
				ConfigPlanChecks: expectEmptyWorkspaceRateLimitPlan(),
			},
			{
				Config:           raised,
				ConfigPlanChecks: expectWorkspaceRateLimitAction(plancheck.ResourceActionUpdate),
				Check:            checkFakeRateLimitOverrides(fake, map[string]int64{"tokens_per_day": 6000000}),
			},
			{
				Config:           raised,
				ConfigPlanChecks: expectEmptyWorkspaceRateLimitPlan(),
			},
			{
				PreConfig:          func() { *now = now.Add(2 * time.Hour) },
				Config:             raised,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.rateLimitListLags = false
					fake.catchUpRateLimitList()
				},
				Config:           raised,
				ConfigPlanChecks: expectEmptyWorkspaceRateLimitPlan(),
			},
		},
	})
}

func TestAccWorkspaceRateLimit_DetectsDriftWhileTheListLags(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)
	useTestClock(t, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))

	fake.mu.Lock()
	fake.rateLimitListLags = true
	fake.mu.Unlock()

	config := workspaceRateLimitConfig(fakeHaikuRateLimitID, `
    tokens_per_day = 5000000`)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					for workspaceID := range fake.workspaces {
						fake.setRateLimitOverride(workspaceID, fakeHaikuRateLimitID, "tokens_per_day", 1)
					}
				},
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestAccWorkspaceRateLimit_DetectsDrift(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := workspaceRateLimitConfig(fakeHaikuRateLimitID, `
    tokens_per_day = 5000000`)
	drift := func() {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		for workspaceID := range fake.workspaces {
			fake.setRateLimitOverride(workspaceID, fakeHaikuRateLimitID, "tokens_per_day", 1)
		}
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:          drift,
				Config:             config,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				Config:           config,
				ConfigPlanChecks: expectWorkspaceRateLimitAction(plancheck.ResourceActionUpdate),
				Check:            checkFakeRateLimitOverrides(fake, map[string]int64{"tokens_per_day": 5000000}),
			},
		},
	})
}

// A refresh after the list shows the write ends the lag window, so the list
// is trusted again even when an override returns to its value before the write.
func TestAccWorkspaceRateLimit_OverrideClearedOutOfBandIsSetAgain(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := workspaceRateLimitConfig(fakeHaikuRateLimitID, `
    tokens_per_day = 5000000`)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			{RefreshState: true},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					for workspaceID := range fake.workspaces {
						delete(fake.workspaceRateLimits[workspaceID], fakeHaikuRateLimitID)
						delete(fake.listedWorkspaceRateLimits[workspaceID], fakeHaikuRateLimitID)
					}
				},
				Config:           config,
				ConfigPlanChecks: expectWorkspaceRateLimitAction(plancheck.ResourceActionUpdate),
				Check:            checkFakeRateLimitOverrides(fake, map[string]int64{"tokens_per_day": 5000000}),
			},
		},
	})
}

func TestAccWorkspaceRateLimit_ImportAdoptsEveryOverride(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	workspaceID := fake.seed("Imported", fakeDataResidency{})
	fake.setRateLimitOverride(workspaceID, fakeOpusRateLimitID, "requests_per_minute", 50)
	fake.setRateLimitOverride(workspaceID, fakeOpusRateLimitID, "tokens_per_day", 900000)
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: seededWorkspaceRateLimitConfig(workspaceID, fakeOpusRateLimitID, `
    requests_per_minute = 50
    tokens_per_day      = 900000`),
				ResourceName:       workspaceRateLimitAddress,
				ImportState:        true,
				ImportStateId:      workspaceID + "/" + fakeOpusRateLimitID,
				ImportStatePersist: true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					attributes := states[0].Attributes
					if attributes["limits.%"] != "2" || attributes["limits.requests_per_minute"] != "50" || attributes["limits.tokens_per_day"] != "900000" {
						return fmt.Errorf("imported limits = %v", attributes)
					}
					return nil
				},
			},
			{
				Config: seededWorkspaceRateLimitConfig(workspaceID, fakeOpusRateLimitID, `
    requests_per_minute = 50
    tokens_per_day      = 900000`),
				ConfigPlanChecks: expectEmptyWorkspaceRateLimitPlan(),
			},
		},
	})
}

func TestAccWorkspaceRateLimit_ImportWithoutOverridesFails(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	workspaceID := fake.seed("Empty", fakeDataResidency{})
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: seededWorkspaceRateLimitConfig(workspaceID, fakeOpusRateLimitID, `
    requests_per_minute = 50`),
				ResourceName:  workspaceRateLimitAddress,
				ImportState:   true,
				ImportStateId: workspaceID + "/" + fakeOpusRateLimitID,
				ExpectError:   regexp.MustCompile(`Cannot import non-existent remote object`),
			},
		},
	})
}

func TestAccWorkspaceRateLimit_RejectsInvalidImportId(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: workspaceRateLimitConfig(fakeHaikuRateLimitID, `
    tokens_per_day = 5000000`),
			},
			{
				ResourceName:  workspaceRateLimitAddress,
				ImportState:   true,
				ImportStateId: "not-a-valid-id",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestAccWorkspaceRateLimit_RejectsLimiterTypesTheGroupDoesNotAccept(t *testing.T) {
	cases := map[string]struct {
		rateLimitID string
		limits      string
	}{
		"web search takes only tool uses": {fakeWebSearchRateLimitID, `tokens_per_day = 1`},
		"batch takes only requests":       {fakeBatchRateLimitID, `tokens_per_day = 1`},
		"model group takes no tool uses":  {fakeHaikuRateLimitID, `tool_uses_per_second = 1`},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			fake := newFakeAdminAPI()
			server := setupTestServer(t, fake)
			setupTestClient(t, server)

			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      workspaceRateLimitConfig(testCase.rateLimitID, testCase.limits),
						PlanOnly:    true,
						ExpectError: regexp.MustCompile(`Limiter Type Not Accepted`),
					},
				},
			})
			if body := lastRateLimitUpdateBody(fake); body != "" {
				t.Fatalf("the plan sent an update %s, want none", body)
			}
		})
	}
}

func TestAccWorkspaceRateLimit_AcceptsLimiterTypesTheGroupAccepts(t *testing.T) {
	cases := map[string]struct {
		rateLimitID string
		limits      string
		want        map[string]int64
	}{
		"web search":  {fakeWebSearchRateLimitID, `tool_uses_per_second = 5`, map[string]int64{"tool_uses_per_second": 5}},
		"batch":       {fakeBatchRateLimitID, `requests_per_minute = 5`, map[string]int64{"requests_per_minute": 5}},
		"model group": {fakeOpusRateLimitID, `input_tokens_per_minute_cache_aware = 0`, map[string]int64{"input_tokens_per_minute_cache_aware": 0}},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			fake := newFakeAdminAPI()
			server := setupTestServer(t, fake)
			setupTestClient(t, server)

			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: workspaceRateLimitConfig(testCase.rateLimitID, testCase.limits),
						Check:  checkFakeRateLimitOverrides(fake, testCase.want),
					},
				},
			})
		})
	}
}

func TestAccWorkspaceRateLimit_UnknownLimiterTypeReachesTheAPI(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      workspaceRateLimitConfig(fakeHaikuRateLimitID, `requests_per_hour = 5`),
				ExpectError: regexp.MustCompile(`(?s)Unable to set workspace rate limits.*requests_per_hour cannot be set`),
			},
		},
	})
}

func TestAccWorkspaceRateLimit_RejectsInvalidLimits(t *testing.T) {
	cases := map[string]struct {
		limits string
		error  string
	}{
		"negative": {`tokens_per_day = -1`, `value must be between 0 and\s+9007199254740991`},
		"too big":  {`tokens_per_day = 9007199254740992`, `value must be between 0 and\s+9007199254740991`},
		"empty":    {``, `map must contain at least 1 elements`},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			server := setupTestServer(t, newFakeAdminAPI())
			setupTestClient(t, server)

			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      workspaceRateLimitConfig(fakeHaikuRateLimitID, testCase.limits),
						PlanOnly:    true,
						ExpectError: regexp.MustCompile(testCase.error),
					},
				},
			})
		})
	}
}

func TestAccWorkspaceRateLimit_UnknownRateLimitFails(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      workspaceRateLimitConfig("rlt_example_missing", `tokens_per_day = 5`),
				ExpectError: regexp.MustCompile(`(?s)Unable to set workspace rate limits.*Rate limit not\s+found`),
			},
		},
	})
}

func TestAccWorkspaceRateLimit_WorkspaceArchivedOutOfBand(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	workspaceID := fake.seed("Archived later", fakeDataResidency{})
	fake.mu.Unlock()

	config := seededWorkspaceRateLimitConfig(workspaceID, fakeHaikuRateLimitID, `
    tokens_per_day = 5000000`)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.archive(workspaceID)
				},
				Config:           config,
				ConfigPlanChecks: expectWorkspaceRateLimitAction(plancheck.ResourceActionCreate),
				ExpectError:      regexp.MustCompile(`(?s)Workspace\s+is\s+archived`),
			},
			{
				Config: testProviderConfig,
				Check: func(s *terraform.State) error {
					if _, ok := s.RootModule().Resources[workspaceRateLimitAddress]; ok {
						return fmt.Errorf("expected the workspace rate limit to be removed from state")
					}
					return nil
				},
			},
		},
	})
}

func TestAccWorkspaceRateLimit_DestroySucceedsOnAnArchivedWorkspace(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	workspaceID := fake.seed("Archived later", fakeDataResidency{})
	fake.rateLimitListServesArchived = true
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: seededWorkspaceRateLimitConfig(workspaceID, fakeHaikuRateLimitID, `
    tokens_per_day = 5000000`),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.archive(workspaceID)
				},
				Config: testProviderConfig,
				Check: func(s *terraform.State) error {
					if _, ok := s.RootModule().Resources[workspaceRateLimitAddress]; ok {
						return fmt.Errorf("expected the workspace rate limit to be destroyed")
					}
					if body := lastRateLimitUpdateBody(fake); !strings.Contains(body, `"value":null`) {
						return fmt.Errorf("destroy did not try to clear the override: %s", body)
					}
					return nil
				},
			},
		},
	})
}
