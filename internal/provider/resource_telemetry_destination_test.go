package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

const telemetryDestinationAddress = "anthropic_telemetry_destination.test"

var testResidency = fakeDataResidency{WorkspaceGeo: "us", AllowedInferenceGeos: "unrestricted", DefaultInferenceGeo: "global"}

func telemetryConfig(attributes string) string {
	return testProviderConfig + "\nresource \"anthropic_telemetry_destination\" \"test\" {\n" + attributes + "\n}\n"
}

func telemetryWorkspacesHCL(kind string, ids ...string) string {
	if len(ids) == 0 {
		return fmt.Sprintf("  workspaces = {\n    type = %q\n  }", kind)
	}
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = fmt.Sprintf("%q", id)
	}
	return fmt.Sprintf("  workspaces = {\n    type          = %q\n    workspace_ids = [%s]\n  }", kind, strings.Join(quoted, ", "))
}

func seedWorkspaces(fake *fakeAdminAPI, count int) []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	ids := make([]string, count)
	for i := range ids {
		ids[i] = fake.seed(fmt.Sprintf("workspace-%d", i+1), testResidency)
	}
	return ids
}

func checkTelemetryDestinationsGone(fake *fakeAdminAPI) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if n := len(fake.telemetryDestinations); n != 0 {
			return fmt.Errorf("expected every telemetry destination to be deleted on destroy, %d remain", n)
		}
		return nil
	}
}

func checkFakeTelemetryDestination(fake *fakeAdminAPI, check func(*fakeTelemetryDestination) error) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		id := s.RootModule().Resources[telemetryDestinationAddress].Primary.ID
		fake.mu.Lock()
		defer fake.mu.Unlock()
		dest, ok := fake.telemetryDestinations[id]
		if !ok {
			return fmt.Errorf("telemetry destination %s is missing from the fake", id)
		}
		return check(dest)
	}
}

func checkFakeTelemetryWorkspaces(fake *fakeAdminAPI, kind string, want ...string) resource.TestCheckFunc {
	return checkFakeTelemetryDestination(fake, func(dest *fakeTelemetryDestination) error {
		got := slices.Clone(dest.WorkspaceIDs)
		expected := slices.Clone(want)
		slices.Sort(got)
		slices.Sort(expected)
		if dest.WorkspacesType != kind || !slices.Equal(got, expected) {
			return fmt.Errorf("workspaces = %s %v, want %s %v", dest.WorkspacesType, got, kind, expected)
		}
		return nil
	})
}

func checkFakeTelemetryHeaders(fake *fakeAdminAPI, want map[string]string) resource.TestCheckFunc {
	return checkFakeTelemetryDestination(fake, func(dest *fakeTelemetryDestination) error {
		if len(dest.Headers) != len(want) {
			return fmt.Errorf("headers = %v, want %v", dest.Headers, want)
		}
		for name, value := range want {
			if dest.Headers[name] != value {
				return fmt.Errorf("headers = %v, want %v", dest.Headers, want)
			}
		}
		return nil
	})
}

// checkLastTelemetryUpdate parses the last update request body so a test can
// assert on what the provider sent, including explicit nulls.
func checkLastTelemetryUpdate(fake *fakeAdminAPI, check func(map[string]any) error) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if len(fake.telemetryUpdateBodies) == 0 {
			return fmt.Errorf("no update request reached the fake")
		}
		last := fake.telemetryUpdateBodies[len(fake.telemetryUpdateBodies)-1]
		var body map[string]any
		if err := json.Unmarshal([]byte(last), &body); err != nil {
			return fmt.Errorf("last update body %q is not JSON: %w", last, err)
		}
		if err := check(body); err != nil {
			return fmt.Errorf("%w (body %s)", err, last)
		}
		return nil
	}
}

func expectNullAt(body map[string]any, keys ...string) error {
	var current any = body
	for i, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return fmt.Errorf("%s is not an object", strings.Join(keys[:i], "."))
		}
		value, present := object[key]
		if !present {
			return fmt.Errorf("%s is not sent", strings.Join(keys[:i+1], "."))
		}
		current = value
	}
	if current != nil {
		return fmt.Errorf("%s = %v, want null", strings.Join(keys, "."), current)
	}
	return nil
}

func expectAbsentAt(body map[string]any, key string) error {
	if _, present := body[key]; present {
		return fmt.Errorf("%s was sent, want it omitted", key)
	}
	return nil
}

func expectTelemetryAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(telemetryDestinationAddress, action)},
	}
}

func captureTelemetryId(id *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		*id = s.RootModule().Resources[telemetryDestinationAddress].Primary.ID
		return nil
	}
}

func TestAccTelemetryDestination_Minimal(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
` + telemetryWorkspacesHCL("all"))

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkTelemetryDestinationsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "id", "tdst_0001"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "endpoint_url", "https://otel.example.com/otlp"),
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "display_name"),
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "headers.%"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "header_names.#", "0"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "enabled", "true"),
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "disabled_reason"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "traces.content_mode", "metadata_only"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "traces.enabled", "true"),
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "traces.endpoint_url"),
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "traces.max_content_bytes"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "workspaces.type", "all"),
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "workspaces.workspace_ids.#"),
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "delivery_health.last_error"),
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "delivery_health.last_success_at"),
					resource.TestCheckResourceAttrSet(telemetryDestinationAddress, "created_at"),
					resource.TestCheckResourceAttrSet(telemetryDestinationAddress, "updated_at"),
				),
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan(),
			},
			{
				ResourceName:      telemetryDestinationAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccTelemetryDestination_Full(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)
	workspaceIds := seedWorkspaces(fake, 2)

	config := telemetryConfig(fmt.Sprintf(`
  endpoint_url = "https://otel.example.com/otlp"
  display_name = "Primary"
  headers = {
    authorization = "Bearer secret"
    x-tenant      = "acme"
  }
  traces = {
    content_mode      = "content"
    max_content_bytes = 4096
    endpoint_url      = "https://otel.example.com/custom/traces"
  }
%s`, telemetryWorkspacesHCL("include", workspaceIds...)))

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkTelemetryDestinationsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "display_name", "Primary"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "headers.%", "2"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "header_names.#", "2"),
					resource.TestCheckTypeSetElemAttr(telemetryDestinationAddress, "header_names.*", "authorization"),
					resource.TestCheckTypeSetElemAttr(telemetryDestinationAddress, "header_names.*", "x-tenant"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "traces.content_mode", "content"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "traces.enabled", "true"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "traces.max_content_bytes", "4096"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "traces.endpoint_url", "https://otel.example.com/custom/traces"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "workspaces.type", "include"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "workspaces.workspace_ids.#", "2"),
					checkFakeTelemetryHeaders(fake, map[string]string{"authorization": "Bearer secret", "x-tenant": "acme"}),
					checkFakeTelemetryWorkspaces(fake, "include", workspaceIds...),
				),
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan(),
			},
			{
				ResourceName:            telemetryDestinationAddress,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"headers"},
			},
		},
	})
}

func TestAccTelemetryDestination_DisplayName(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := func(displayName string) string {
		return telemetryConfig(displayName + `
  endpoint_url = "https://otel.example.com/otlp"
` + telemetryWorkspacesHCL("all"))
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkTelemetryDestinationsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: config(`display_name = "First"`),
				Check:  resource.TestCheckResourceAttr(telemetryDestinationAddress, "display_name", "First"),
			},
			{
				Config:           config(`display_name = "Second"`),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "display_name", "Second"),
					checkFakeTelemetryDestination(fake, func(d *fakeTelemetryDestination) error {
						if d.DisplayName == nil || *d.DisplayName != "Second" {
							return fmt.Errorf("display_name = %v, want Second", d.DisplayName)
						}
						return nil
					}),
				),
			},
			{
				Config:           config(""),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "display_name"),
					checkLastTelemetryUpdate(fake, func(body map[string]any) error { return expectNullAt(body, "display_name") }),
					checkFakeTelemetryDestination(fake, func(d *fakeTelemetryDestination) error {
						if d.DisplayName != nil {
							return fmt.Errorf("display_name = %q, want it cleared", *d.DisplayName)
						}
						return nil
					}),
				),
			},
		},
	})
}

func TestAccTelemetryDestination_Headers(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := func(headers string) string {
		return telemetryConfig(headers + `
  endpoint_url = "https://otel.example.com/otlp"
` + telemetryWorkspacesHCL("all"))
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkTelemetryDestinationsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: config(`headers = { a = "1", b = "2" }`),
				Check:  checkFakeTelemetryHeaders(fake, map[string]string{"a": "1", "b": "2"}),
			},
			{
				Config:           config(`headers = { a = "9", b = "2", c = "3" }`),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "header_names.#", "3"),
					checkFakeTelemetryHeaders(fake, map[string]string{"a": "9", "b": "2", "c": "3"}),
				),
			},
			{
				Config:           config(`headers = { a = "9", c = "3" }`),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkLastTelemetryUpdate(fake, func(body map[string]any) error {
						return expectNullAt(body, "headers", "b")
					}),
					checkFakeTelemetryHeaders(fake, map[string]string{"a": "9", "c": "3"}),
				),
			},
			{
				Config:           config(""),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "header_names.#", "0"),
					checkLastTelemetryUpdate(fake, func(body map[string]any) error {
						if err := expectNullAt(body, "headers", "a"); err != nil {
							return err
						}
						return expectNullAt(body, "headers", "c")
					}),
					checkFakeTelemetryHeaders(fake, map[string]string{}),
				),
			},
		},
	})
}

func headersWoConfig(endpointURL, headers string, version int, extra string) string {
	return telemetryConfig(fmt.Sprintf(`
  endpoint_url       = %q
  headers_wo         = { %s }
  headers_wo_version = %d
%s
`, endpointURL, headers, version, extra) + telemetryWorkspacesHCL("all"))
}

func checkLastTelemetryUpdateOmitsHeaders(fake *fakeAdminAPI) resource.TestCheckFunc {
	return checkLastTelemetryUpdate(fake, func(body map[string]any) error {
		return expectAbsentAt(body, "headers")
	})
}

func TestAccTelemetryDestination_HeadersWriteOnly(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	const url = "https://otel.example.com/otlp"
	const displayName = "  display_name = \"Renamed\""

	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkTelemetryDestinationsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: headersWoConfig(url, `a = "1", b = "2"`, 1, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "headers_wo_version", "1"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "header_names.#", "2"),
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "headers_wo.%"),
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "headers.%"),
					checkFakeTelemetryHeaders(fake, map[string]string{"a": "1", "b": "2"}),
				),
			},
			{
				Config:           headersWoConfig(url, `a = "1", b = "2"`, 1, ""),
				ConfigPlanChecks: expectEmptyPlan(),
			},
			{
				Config:           headersWoConfig(url, `a = "changed", b = "2"`, 1, ""),
				ConfigPlanChecks: expectEmptyPlan(),
				Check:            checkFakeTelemetryHeaders(fake, map[string]string{"a": "1", "b": "2"}),
			},
			{
				Config:           headersWoConfig(url, `a = "1", b = "2"`, 1, displayName),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkLastTelemetryUpdateOmitsHeaders(fake),
					checkFakeTelemetryHeaders(fake, map[string]string{"a": "1", "b": "2"}),
				),
			},
			{
				Config:           headersWoConfig(url, `a = "rotated"`, 2, displayName),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "headers_wo_version", "2"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "header_names.#", "1"),
					checkLastTelemetryUpdate(fake, func(body map[string]any) error {
						return expectNullAt(body, "headers", "b")
					}),
					checkFakeTelemetryHeaders(fake, map[string]string{"a": "rotated"}),
				),
			},
			{
				Config:           headersWoConfig("https://other.example.com/otlp", `a = "rotated"`, 2, displayName),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check:            checkFakeTelemetryHeaders(fake, map[string]string{"a": "rotated"}),
			},
			{
				Config: telemetryConfig(`
  endpoint_url = "https://other.example.com/otlp"
` + displayName + `
` + telemetryWorkspacesHCL("all")),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "headers_wo_version"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "header_names.#", "0"),
					checkFakeTelemetryHeaders(fake, map[string]string{}),
				),
			},
		},
	})
}

func TestAccTelemetryDestination_HeadersToHeadersWriteOnly(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	const url = "https://otel.example.com/otlp"

	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkTelemetryDestinationsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: telemetryConfig(`
  endpoint_url = "` + url + `"
  headers      = { a = "1", b = "2" }
` + telemetryWorkspacesHCL("all")),
				Check: resource.TestCheckResourceAttr(telemetryDestinationAddress, "headers.%", "2"),
			},
			{
				Config:           headersWoConfig(url, `a = "1"`, 1, ""),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "headers.%"),
					checkLastTelemetryUpdate(fake, func(body map[string]any) error {
						return expectNullAt(body, "headers", "b")
					}),
					checkFakeTelemetryHeaders(fake, map[string]string{"a": "1"}),
				),
			},
			{
				Config: telemetryConfig(`
  endpoint_url = "` + url + `"
  headers      = { a = "2" }
` + telemetryWorkspacesHCL("all")),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "headers.%", "1"),
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "headers_wo_version"),
					checkFakeTelemetryHeaders(fake, map[string]string{"a": "2"}),
				),
			},
		},
	})
}

func TestAccTelemetryDestination_HeadersWriteOnlyAfterImport(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	const url = "https://otel.example.com/otlp"
	config := headersWoConfig(url, `a = "1"`, 1, "")

	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkTelemetryDestinationsGone(fake),
		Steps: []resource.TestStep{
			{Config: config},
			{
				ResourceName:            telemetryDestinationAddress,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"headers_wo_version"},
			},
			{
				Config: testProviderConfig + `
removed {
  from = anthropic_telemetry_destination.test
  lifecycle {
    destroy = false
  }
}
`,
			},
			{
				Config:             config,
				ImportState:        true,
				ImportStateId:      "tdst_0001",
				ImportStatePersist: true,
				ResourceName:       telemetryDestinationAddress,
			},
			{
				Config:           config,
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "headers_wo_version", "1"),
					checkFakeTelemetryHeaders(fake, map[string]string{"a": "1"}),
				),
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyPlan(),
			},
		},
	})
}

func TestAccTelemetryDestination_EndpointURL(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := func(endpoint string) string {
		return telemetryConfig(fmt.Sprintf(`
  endpoint_url = %q
  headers = {
    authorization = "Bearer secret"
  }
%s`, endpoint, telemetryWorkspacesHCL("all")))
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkTelemetryDestinationsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: config("https://a.example.com/otlp"),
				Check:  checkFakeTelemetryHeaders(fake, map[string]string{"authorization": "Bearer secret"}),
			},
			{
				Config:           config("https://a.example.com/other"),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "endpoint_url", "https://a.example.com/other"),
					checkLastTelemetryUpdate(fake, func(body map[string]any) error { return expectAbsentAt(body, "headers") }),
					checkFakeTelemetryHeaders(fake, map[string]string{"authorization": "Bearer secret"}),
				),
			},
			{
				Config:           config("https://b.example.com/other"),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "header_names.#", "1"),
					checkLastTelemetryUpdate(fake, func(body map[string]any) error {
						headers, _ := body["headers"].(map[string]any)
						if headers["authorization"] != "Bearer secret" {
							return fmt.Errorf("headers = %v, want authorization sent again", headers)
						}
						return nil
					}),
					checkFakeTelemetryHeaders(fake, map[string]string{"authorization": "Bearer secret"}),
				),
			},
			{
				Config:           config("https://b.example.com/other"),
				ConfigPlanChecks: expectEmptyPlan(),
			},
		},
	})
}

func TestAccTelemetryDestination_Traces(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := func(traces string) string {
		return telemetryConfig(traces + `
  endpoint_url = "https://otel.example.com/otlp"
` + telemetryWorkspacesHCL("all"))
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkTelemetryDestinationsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: config(`traces = {
    content_mode      = "content"
    max_content_bytes = 1000
    endpoint_url      = "https://otel.example.com/custom"
  }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "traces.enabled", "true"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "traces.max_content_bytes", "1000"),
				),
			},
			{
				Config:           config(`traces = { content_mode = "content" }`),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "traces.max_content_bytes"),
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "traces.endpoint_url"),
					checkLastTelemetryUpdate(fake, func(body map[string]any) error {
						if err := expectNullAt(body, "traces", "endpoint_url"); err != nil {
							return err
						}
						return expectNullAt(body, "traces", "max_content_bytes")
					}),
					checkFakeTelemetryDestination(fake, func(d *fakeTelemetryDestination) error {
						if d.TracesEndpoint != nil || d.MaxContentBytes != nil {
							return fmt.Errorf("traces endpoint_url and max_content_bytes should be removed, got %v and %v", d.TracesEndpoint, d.MaxContentBytes)
						}
						return nil
					}),
				),
			},
			{
				Config:           config(`traces = { enabled = false, content_mode = "metadata_only" }`),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "traces.enabled", "false"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "traces.content_mode", "metadata_only"),
				),
			},
			{
				Config:           config(""),
				ConfigPlanChecks: expectEmptyPlan(),
				Check:            resource.TestCheckResourceAttr(telemetryDestinationAddress, "traces.enabled", "false"),
			},
		},
	})
}

func TestAccTelemetryDestination_Enabled(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := func(enabled string) string {
		return telemetryConfig(enabled + `
  endpoint_url = "https://otel.example.com/otlp"
` + telemetryWorkspacesHCL("all"))
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkTelemetryDestinationsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: config("enabled = false"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "enabled", "false"),
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "disabled_reason", "caller"),
				),
			},
			{
				Config:           config(""),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "enabled", "true"),
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "disabled_reason"),
				),
			},
			{
				Config:           config("enabled = false"),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check:            resource.TestCheckResourceAttr(telemetryDestinationAddress, "disabled_reason", "caller"),
			},
		},
	})
}

func TestAccTelemetryDestination_DisabledByAPI(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	var id string
	config := telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
` + telemetryWorkspacesHCL("all"))

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkTelemetryDestinationsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  captureTelemetryId(&id),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.disableTelemetryDestination(id, "sustained_failure")
				},
				Config:           config,
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "enabled", "true"),
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "disabled_reason"),
					checkFakeTelemetryDestination(fake, func(d *fakeTelemetryDestination) error {
						if d.Disabled {
							return fmt.Errorf("expected the destination to be enabled again")
						}
						return nil
					}),
				),
			},
		},
	})
}

func TestAccTelemetryDestination_DeletedOutOfBand(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	var id string
	config := telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
` + telemetryWorkspacesHCL("all"))

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkTelemetryDestinationsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  captureTelemetryId(&id),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.removeTelemetryDestination(id)
				},
				Config:           config,
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionCreate),
				Check:            resource.TestCheckResourceAttr(telemetryDestinationAddress, "id", "tdst_0002"),
			},
		},
	})
}

func TestAccTelemetryDestination_HeaderRemovedOutOfBand(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	var id string
	config := telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
  headers = {
    a = "1"
    b = "2"
  }
` + telemetryWorkspacesHCL("all"))

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkTelemetryDestinationsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  captureTelemetryId(&id),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.removeTelemetryDestinationHeader(id, "b")
				},
				Config:           config,
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "header_names.#", "2"),
					checkFakeTelemetryHeaders(fake, map[string]string{"a": "1", "b": "2"}),
				),
			},
		},
	})
}

func TestAccTelemetryDestination_Workspaces(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)
	ids := seedWorkspaces(fake, 4)

	config := func(kind string, workspaceIds ...string) string {
		return telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
` + telemetryWorkspacesHCL(kind, workspaceIds...))
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkTelemetryDestinationsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: config("all"),
				Check:  checkFakeTelemetryWorkspaces(fake, "all"),
			},
			{
				Config:           config("include", ids[0], ids[1]),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "workspaces.workspace_ids.#", "2"),
					checkFakeTelemetryWorkspaces(fake, "include", ids[0], ids[1]),
				),
			},
			{
				Config:           config("include", ids[1], ids[2]),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkFakeTelemetryWorkspaces(fake, "include", ids[1], ids[2]),
				),
			},
			{
				Config:           config("exclude", ids[3]),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check:            checkFakeTelemetryWorkspaces(fake, "exclude", ids[3]),
			},
			{
				Config:           config("include"),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(telemetryDestinationAddress, "workspaces.workspace_ids.#"),
					checkFakeTelemetryWorkspaces(fake, "include"),
				),
			},
			{
				Config:           config("all"),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check:            checkFakeTelemetryWorkspaces(fake, "all"),
			},
		},
	})
}

func TestAccTelemetryDestination_ManyWorkspaces(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)
	ids := seedWorkspaces(fake, 150)

	config := func(workspaceIds []string) string {
		return telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
` + telemetryWorkspacesHCL("include", workspaceIds...))
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkTelemetryDestinationsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: config(ids),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationAddress, "workspaces.workspace_ids.#", "150"),
					checkFakeTelemetryWorkspaces(fake, "include", ids...),
				),
			},
			{
				Config:           config(ids),
				ConfigPlanChecks: expectEmptyPlan(),
			},
			{
				Config:           config(ids[:120]),
				ConfigPlanChecks: expectTelemetryAction(plancheck.ResourceActionUpdate),
				Check:            checkFakeTelemetryWorkspaces(fake, "include", ids[:120]...),
			},
		},
	})
}

func TestAccTelemetryDestination_Conflict(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := func(name string) string {
		return telemetryConfig(fmt.Sprintf(`
  display_name = %q
  endpoint_url = "https://otel.example.com/otlp"
%s`, name, telemetryWorkspacesHCL("all")))
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkTelemetryDestinationsGone(fake),
		Steps: []resource.TestStep{
			{Config: config("First")},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.telemetryConflicts = true
				},
				Config:      config("Second"),
				ExpectError: regexp.MustCompile(`(?s)Conflict.*terraform apply`),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.telemetryConflicts = false
				},
				Config: config("Second"),
				Check:  resource.TestCheckResourceAttr(telemetryDestinationAddress, "display_name", "Second"),
			},
		},
	})
}

func TestAccTelemetryDestination_Validation(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	tooMany := make([]string, 1001)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("wrkspc_%04d", i)
	}

	cases := []struct {
		name   string
		config string
		expect string
	}{
		{
			"workspace_ids with type all",
			telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
  workspaces = {
    type          = "all"
    workspace_ids = ["wrkspc_0001"]
  }`),
			`Invalid Workspaces`,
		},
		{
			"unknown workspaces type",
			telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
` + telemetryWorkspacesHCL("some")),
			`(?s)value must be one of.*all`,
		},
		{
			"empty workspace_ids",
			telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
  workspaces = {
    type          = "include"
    workspace_ids = []
  }`),
			`(?s)at least 1`,
		},
		{
			"more than 1000 workspace_ids",
			telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
` + telemetryWorkspacesHCL("include", tooMany...)),
			`(?s)at most 1000|1000`,
		},
		{
			"too many headers",
			telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
  headers = {` + manyHeaders(21) + `}
` + telemetryWorkspacesHCL("all")),
			`(?s)at most 20|20`,
		},
		{
			"headers with headers_wo",
			telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
  headers      = { a = "1" }
  headers_wo   = { a = "1" }
  headers_wo_version = 1
` + telemetryWorkspacesHCL("all")),
			`(?s)Invalid Attribute Combination|cannot be specified`,
		},
		{
			"headers_wo without version",
			telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
  headers_wo   = { a = "1" }
` + telemetryWorkspacesHCL("all")),
			`(?s)Invalid Attribute Combination|must be specified`,
		},
		{
			"version without headers_wo",
			telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
  headers_wo_version = 1
` + telemetryWorkspacesHCL("all")),
			`(?s)Invalid Attribute Combination|must be specified`,
		},
		{
			"too many headers_wo",
			telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
  headers_wo = {` + manyHeaders(21) + `}
  headers_wo_version = 1
` + telemetryWorkspacesHCL("all")),
			`(?s)at most 20|20`,
		},
		{
			"unknown content_mode",
			telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
  traces = { content_mode = "everything" }
` + telemetryWorkspacesHCL("all")),
			`(?s)value must be one of.*metadata_only`,
		},
		{
			"http endpoint is rejected by the API",
			telemetryConfig(`
  endpoint_url = "http://otel.example.com/otlp"
` + telemetryWorkspacesHCL("all")),
			`(?s)API Error.*https`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{Config: tc.config, ExpectError: regexp.MustCompile(tc.expect)},
				},
			})
		})
	}
}

func manyHeaders(count int) string {
	var b strings.Builder
	for i := range count {
		fmt.Fprintf(&b, "\n    h%d = \"v\"", i)
	}
	return b.String() + "\n  "
}

func TestAccTelemetryDestination_LimitReached(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	for range fakeTelemetryMaxDestinations {
		fake.seedTelemetryDestination(fakeTelemetryDestinationSeed{})
	}
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: telemetryConfig(`
  endpoint_url = "https://otel.example.com/otlp"
` + telemetryWorkspacesHCL("all")),
				ExpectError: regexp.MustCompile(`(?s)API Error.*at most 10`),
			},
		},
	})
}

func TestFakeTelemetryDestinations_Semantics(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)

	do := func(method, path, body string, beta bool) (int, map[string]any) {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Api-Key", testAPIKey)
		req.Header.Set("Content-Type", "application/json")
		if beta {
			req.Header.Set("anthropic-beta", fakeTelemetryBeta)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	headerNames := func(out map[string]any) []string {
		var names []string
		for _, n := range out["header_names"].([]any) {
			names = append(names, n.(string))
		}
		slices.Sort(names)
		return names
	}

	const base = "/v1/organizations/telemetry_destinations"

	if status, _ := do(http.MethodGet, base, "", false); status != http.StatusBadRequest {
		t.Fatalf("a request without the beta header returned %d, want 400", status)
	}

	status, created := do(http.MethodPost, base, `{"endpoint_url":"https://a.example.com","headers":{"a":"1","b":"2"},"workspaces":{"type":"all"}}`, true)
	if status != http.StatusOK {
		t.Fatalf("create returned %d: %v", status, created)
	}
	id := created["id"].(string)

	if status, _ := do(http.MethodPost, base+"/"+id, `{"headers":{"c":"3"}}`, true); status != http.StatusBadRequest {
		t.Fatalf("setting a header without endpoint_url returned %d, want 400", status)
	}

	status, merged := do(http.MethodPost, base+"/"+id, `{"endpoint_url":"https://a.example.com","headers":{"c":"3","b":null}}`, true)
	if status != http.StatusOK || !slices.Equal(headerNames(merged), []string{"a", "c"}) {
		t.Fatalf("merge returned %d with header names %v, want a and c", status, merged["header_names"])
	}

	status, removed := do(http.MethodPost, base+"/"+id, `{"headers":{"a":null}}`, true)
	if status != http.StatusOK || !slices.Equal(headerNames(removed), []string{"c"}) {
		t.Fatalf("removal returned %d with header names %v, want c", status, removed["header_names"])
	}

	status, wiped := do(http.MethodPost, base+"/"+id, `{"endpoint_url":"https://other.example.com"}`, true)
	if status != http.StatusOK || len(headerNames(wiped)) != 0 {
		t.Fatalf("host change returned %d with header names %v, want none", status, wiped["header_names"])
	}

	if status, _ := do(http.MethodPost, base, `{"endpoint_url":"https://a.example.com","workspaces":{"type":"all","workspace_ids":["wrkspc_1"]}}`, true); status != http.StatusBadRequest {
		t.Fatalf("workspace_ids with type all returned %d, want 400", status)
	}

	ids := make([]string, 101)
	for i := range ids {
		ids[i] = fmt.Sprintf("%q", fmt.Sprintf("wrkspc_%d", i))
	}
	body := `{"endpoint_url":"https://a.example.com","workspaces":{"type":"include","workspace_ids":[` + strings.Join(ids, ",") + `]}}`
	if status, _ := do(http.MethodPost, base, body, true); status != http.StatusBadRequest {
		t.Fatalf("101 workspace IDs returned %d, want 400", status)
	}
}
