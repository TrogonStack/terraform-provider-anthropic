package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const spendLimitAddress = "anthropic_spend_limit.test"

func spendLimitConfig(userID, body string) string {
	return testProviderConfig + fmt.Sprintf(`
resource "anthropic_spend_limit" "test" {
  user_id = %q
%s
}
`, userID, body)
}

func expectSpendLimitAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(spendLimitAddress, action)},
	}
}

func expectEmptySpendLimitPlan() resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}

func captureSpendLimitId(id *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		*id = s.RootModule().Resources[spendLimitAddress].Primary.ID
		return nil
	}
}

func checkSpendLimitId(id *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		if got := s.RootModule().Resources[spendLimitAddress].Primary.ID; got != *id {
			return fmt.Errorf("id = %s, want %s kept by the upsert", got, *id)
		}
		return nil
	}
}

func checkFakeSpendLimitAmount(fake *fakeAdminAPI, userID string, want *string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		limit, ok := fake.spendLimitOverride(userID, "monthly")
		if !ok {
			return fmt.Errorf("no monthly override for %s", userID)
		}
		switch {
		case want == nil && limit.Amount != nil:
			return fmt.Errorf("amount = %s, want null", *limit.Amount)
		case want != nil && (limit.Amount == nil || *limit.Amount != *want):
			return fmt.Errorf("amount = %v, want %s", limit.Amount, *want)
		}
		return nil
	}
}

func checkLastSpendLimitSetBodyContains(fake *fakeAdminAPI, fragments ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		body := fake.lastSpendLimitSetBody()
		for _, fragment := range fragments {
			if !strings.Contains(body, fragment) {
				return fmt.Errorf("last set body %s does not contain %s", body, fragment)
			}
		}
		return nil
	}
}

func checkSpendLimitOverridesGone(fake *fakeAdminAPI) resource.TestCheckFunc {
	return func(*terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if n := len(fake.spendLimits.overrides); n != 0 {
			return fmt.Errorf("%d spend limit overrides remain after destroy", n)
		}
		return nil
	}
}

func TestAccSpendLimit_Basic(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	var id string
	created := spendLimitConfig(fakeSpendLimitMemberAda, `  amount = "50000"`)
	raised := spendLimitConfig(fakeSpendLimitMemberAda, `  amount = "75000"`)
	unlimited := spendLimitConfig(fakeSpendLimitMemberAda, ``)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkSpendLimitOverridesGone(fake),
		Steps: []resource.TestStep{
			{
				Config: created,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr(spendLimitAddress, "id", regexp.MustCompile(`^spl_`)),
					resource.TestCheckResourceAttr(spendLimitAddress, "user_id", fakeSpendLimitMemberAda),
					resource.TestCheckResourceAttr(spendLimitAddress, "period", "monthly"),
					resource.TestCheckResourceAttr(spendLimitAddress, "amount", "50000"),
					resource.TestCheckResourceAttr(spendLimitAddress, "currency", "USD"),
					resource.TestCheckResourceAttrSet(spendLimitAddress, "created_at"),
					resource.TestCheckResourceAttrSet(spendLimitAddress, "updated_at"),
					checkFakeSpendLimitAmount(fake, fakeSpendLimitMemberAda, fakeStringPointer("50000")),
					checkLastSpendLimitSetBodyContains(fake, `"amount":"50000"`, `"scope":{"user_id":"`+fakeSpendLimitMemberAda+`","type":"user"}`),
					captureSpendLimitId(&id),
				),
			},
			{
				Config:           created,
				ConfigPlanChecks: expectEmptySpendLimitPlan(),
			},
			{
				ResourceName:      spendLimitAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:           raised,
				ConfigPlanChecks: expectSpendLimitAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(spendLimitAddress, "amount", "75000"),
					checkSpendLimitId(&id),
					checkFakeSpendLimitAmount(fake, fakeSpendLimitMemberAda, fakeStringPointer("75000")),
					checkLastSpendLimitSetBodyContains(fake, `"period":"monthly"`),
				),
			},
			{
				Config:           unlimited,
				ConfigPlanChecks: expectSpendLimitAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(spendLimitAddress, "amount"),
					checkSpendLimitId(&id),
					checkFakeSpendLimitAmount(fake, fakeSpendLimitMemberAda, nil),
					checkLastSpendLimitSetBodyContains(fake, `"amount":null`),
				),
			},
			{
				Config:           unlimited,
				ConfigPlanChecks: expectEmptySpendLimitPlan(),
			},
			{
				ResourceName:      spendLimitAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccSpendLimit_CreateAdoptsTheExistingOverride(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	existing := fake.setSpendLimitOutOfBand(fakeSpendLimitMemberGrace, "monthly", fakeStringPointer("1000")).ID
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkSpendLimitOverridesGone(fake),
		Steps: []resource.TestStep{
			{
				Config: spendLimitConfig(fakeSpendLimitMemberGrace, `
  period = "monthly"
  amount = "2000"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(spendLimitAddress, "id", existing),
					checkFakeSpendLimitAmount(fake, fakeSpendLimitMemberGrace, fakeStringPointer("2000")),
				),
			},
		},
	})
}

func TestAccSpendLimit_DetectsDrift(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := spendLimitConfig(fakeSpendLimitMemberAda, `  amount = "50000"`)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.setSpendLimitOutOfBand(fakeSpendLimitMemberAda, "monthly", nil)
				},
				Config:           config,
				ConfigPlanChecks: expectSpendLimitAction(plancheck.ResourceActionUpdate),
				Check:            checkFakeSpendLimitAmount(fake, fakeSpendLimitMemberAda, fakeStringPointer("50000")),
			},
		},
	})
}

func TestAccSpendLimit_DeletedOutOfBand(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	var id string
	config := spendLimitConfig(fakeSpendLimitMemberAda, `  amount = "50000"`)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkSpendLimitOverridesGone(fake),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  captureSpendLimitId(&id),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					delete(fake.spendLimits.overrides, id)
				},
				Config:           config,
				ConfigPlanChecks: expectSpendLimitAction(plancheck.ResourceActionCreate),
				Check: resource.ComposeAggregateTestCheckFunc(
					func(s *terraform.State) error {
						if got := s.RootModule().Resources[spendLimitAddress].Primary.ID; got == id {
							return fmt.Errorf("id = %s, want a new spend limit", got)
						}
						return nil
					},
					checkFakeSpendLimitAmount(fake, fakeSpendLimitMemberAda, fakeStringPointer("50000")),
				),
			},
		},
	})
}

func TestAccSpendLimit_DestroyIgnoresNotFound(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkSpendLimitOverridesGone(fake),
		Steps: []resource.TestStep{
			{Config: spendLimitConfig(fakeSpendLimitMemberAda, `  amount = "50000"`)},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.spendLimits.deleteRaces = true
				},
				Config: testProviderConfig,
			},
		},
	})
}

func TestAccSpendLimit_ChangingThePeriodReplaces(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: spendLimitConfig(fakeSpendLimitMemberAda, `  amount = "50000"`)},
			{
				Config: spendLimitConfig(fakeSpendLimitMemberAda, `
  period = "weekly"
  amount = "50000"`),
				ConfigPlanChecks: expectSpendLimitAction(plancheck.ResourceActionDestroyBeforeCreate),
				ExpectError:      regexp.MustCompile(`period: only monthly`),
			},
		},
	})
}

func TestAccSpendLimit_ChangingTheUserReplaces(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkSpendLimitOverridesGone(fake),
		Steps: []resource.TestStep{
			{Config: spendLimitConfig(fakeSpendLimitMemberAda, `  amount = "50000"`)},
			{
				Config:           spendLimitConfig(fakeSpendLimitMemberGrace, `  amount = "50000"`),
				ConfigPlanChecks: expectSpendLimitAction(plancheck.ResourceActionDestroyBeforeCreate),
				Check: func(*terraform.State) error {
					if _, ok := fake.spendLimitOverride(fakeSpendLimitMemberAda, "monthly"); ok {
						return fmt.Errorf("the previous user's override was not deleted")
					}
					return checkFakeSpendLimitAmount(fake, fakeSpendLimitMemberGrace, fakeStringPointer("50000"))(nil)
				},
			},
		},
	})
}

func TestAccSpendLimit_ImportRejectsANonUserScope(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        spendLimitConfig(fakeSpendLimitMemberAda, `  amount = "20000"`),
				ResourceName:  spendLimitAddress,
				ImportState:   true,
				ImportStateId: "spl_example_organization_monthly",
				ExpectError:   regexp.MustCompile(`(?s)Unsupported Spend Limit Scope.*organization scope`),
			},
		},
	})
}

func TestAccSpendLimit_ImportUnknownIdFails(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        spendLimitConfig(fakeSpendLimitMemberAda, `  amount = "20000"`),
				ResourceName:  spendLimitAddress,
				ImportState:   true,
				ImportStateId: "spl_example_missing",
				ExpectError:   regexp.MustCompile(`Cannot import non-existent remote object`),
			},
		},
	})
}

func TestAccSpendLimit_RejectsAnAmountThatIsNotMinorUnits(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	for _, amount := range []string{"500.00", "-1", "0050", ""} {
		t.Run(amount, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      spendLimitConfig(fakeSpendLimitMemberAda, fmt.Sprintf(`  amount = %q`, amount)),
						PlanOnly:    true,
						ExpectError: regexp.MustCompile(`must be a non-negative integer`),
					},
				},
			})
		})
	}
}
