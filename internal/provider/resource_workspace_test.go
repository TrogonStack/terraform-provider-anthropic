package provider

import (
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const workspaceAddress = "anthropic_workspace.test"

func checkWorkspacesArchived(fake *fakeAdminAPI) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		for id, ws := range fake.workspaces {
			if ws.ArchivedAt == nil {
				return fmt.Errorf("expected workspace %s to be archived on destroy", id)
			}
		}
		return nil
	}
}

func checkFakeWorkspace(fake *fakeAdminAPI, check func(*fakeWorkspace) error) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		id := s.RootModule().Resources[workspaceAddress].Primary.ID
		fake.mu.Lock()
		defer fake.mu.Unlock()
		ws, ok := fake.workspaces[id]
		if !ok {
			return fmt.Errorf("workspace %s is missing from the fake", id)
		}
		return check(ws)
	}
}

func checkFakeTags(fake *fakeAdminAPI, want map[string]string) resource.TestCheckFunc {
	return checkFakeWorkspace(fake, func(ws *fakeWorkspace) error {
		if !maps.Equal(ws.Tags, want) {
			return fmt.Errorf("tags = %v, want %v", ws.Tags, want)
		}
		return nil
	})
}

func checkFakeAllowedGeos(fake *fakeAdminAPI, want any) resource.TestCheckFunc {
	return checkFakeWorkspace(fake, func(ws *fakeWorkspace) error {
		if !reflect.DeepEqual(ws.DataResidency.AllowedInferenceGeos, want) {
			return fmt.Errorf("allowed_inference_geos = %#v, want %#v", ws.DataResidency.AllowedInferenceGeos, want)
		}
		return nil
	})
}

func captureId(id *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		*id = s.RootModule().Resources[workspaceAddress].Primary.ID
		return nil
	}
}

func checkIdChanged(previous *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		if id := s.RootModule().Resources[workspaceAddress].Primary.ID; id == *previous {
			return fmt.Errorf("expected a new workspace, still %s", id)
		}
		return nil
	}
}

func expectAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction(workspaceAddress, action),
		},
	}
}

func expectEmptyPlan() resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}

func TestAccWorkspace_Basic(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkWorkspacesArchived(fake),
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name = "Production"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceAddress, "id", "wrkspc_0001"),
					resource.TestCheckResourceAttr(workspaceAddress, "name", "Production"),
					resource.TestCheckResourceAttr(workspaceAddress, "display_color", "#D97757"),
					resource.TestCheckResourceAttr(workspaceAddress, "tags.%", "0"),
					resource.TestCheckNoResourceAttr(workspaceAddress, "external_key_id"),
					resource.TestCheckResourceAttr(workspaceAddress, "data_residency.workspace_geo", "us"),
					resource.TestCheckNoResourceAttr(workspaceAddress, "data_residency.allowed_inference_geos"),
					resource.TestCheckResourceAttr(workspaceAddress, "data_residency.default_inference_geo", "global"),
					resource.TestCheckResourceAttr(workspaceAddress, "compartment_id", "compartment-0001"),
					resource.TestCheckResourceAttr(workspaceAddress, "created_at", "2024-10-30T23:58:27Z"),
				),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name = "Production"
}
`,
				ConfigPlanChecks: expectEmptyPlan(),
			},
			{
				ResourceName:      workspaceAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name          = "Production EU"
  display_color = "#4A90A4"
  tags = {
    env  = "prod"
    team = "platform"
  }
}
`,
				ConfigPlanChecks: expectAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceAddress, "name", "Production EU"),
					resource.TestCheckResourceAttr(workspaceAddress, "display_color", "#4A90A4"),
					resource.TestCheckResourceAttr(workspaceAddress, "tags.%", "2"),
					resource.TestCheckResourceAttr(workspaceAddress, "tags.env", "prod"),
					checkFakeTags(fake, map[string]string{"env": "prod", "team": "platform"}),
				),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name          = "Production EU"
  display_color = "#4A90A4"
  tags = {
    env = "staging"
  }
}
`,
				ConfigPlanChecks: expectAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceAddress, "tags.%", "1"),
					resource.TestCheckResourceAttr(workspaceAddress, "tags.env", "staging"),
					checkFakeTags(fake, map[string]string{"env": "staging"}),
				),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name = "Production EU"
}
`,
				ConfigPlanChecks: expectAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceAddress, "display_color", "#4A90A4"),
					resource.TestCheckResourceAttr(workspaceAddress, "tags.%", "0"),
					checkFakeTags(fake, map[string]string{}),
				),
			},
		},
	})
}

func TestAccWorkspace_DataResidency(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	var firstId string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkWorkspacesArchived(fake),
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name = "Residency"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					checkFakeAllowedGeos(fake, "unrestricted"),
					captureId(&firstId),
				),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name = "Residency"
  data_residency = {
    allowed_inference_geos = ["us"]
    default_inference_geo  = "us"
  }
}
`,
				ConfigPlanChecks: expectAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceAddress, "id", "wrkspc_0001"),
					resource.TestCheckResourceAttr(workspaceAddress, "data_residency.workspace_geo", "us"),
					resource.TestCheckResourceAttr(workspaceAddress, "data_residency.allowed_inference_geos.#", "1"),
					resource.TestCheckTypeSetElemAttr(workspaceAddress, "data_residency.allowed_inference_geos.*", "us"),
					resource.TestCheckResourceAttr(workspaceAddress, "data_residency.default_inference_geo", "us"),
					checkFakeAllowedGeos(fake, []string{"us"}),
				),
			},
			{
				ResourceName:      workspaceAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name = "Residency"
}
`,
				ConfigPlanChecks: expectEmptyPlan(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceAddress, "data_residency.allowed_inference_geos.#", "1"),
					resource.TestCheckResourceAttr(workspaceAddress, "data_residency.default_inference_geo", "us"),
					checkFakeAllowedGeos(fake, []string{"us"}),
				),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name = "Residency"
  data_residency = {
    default_inference_geo = "global"
  }
}
`,
				ConfigPlanChecks: expectAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceAddress, "id", "wrkspc_0001"),
					resource.TestCheckResourceAttr(workspaceAddress, "data_residency.workspace_geo", "us"),
					resource.TestCheckNoResourceAttr(workspaceAddress, "data_residency.allowed_inference_geos"),
					resource.TestCheckResourceAttr(workspaceAddress, "data_residency.default_inference_geo", "global"),
					checkFakeAllowedGeos(fake, "unrestricted"),
				),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.workspaces[firstId].DataResidency.WorkspaceGeo = "elsewhere"
				},
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name = "Residency"
}
`,
				ConfigPlanChecks: expectEmptyPlan(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceAddress, "id", "wrkspc_0001"),
					resource.TestCheckResourceAttr(workspaceAddress, "data_residency.workspace_geo", "elsewhere"),
				),
			},
		},
	})
}

func TestAccWorkspace_ImportKeepsDataResidency(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	importedId := fake.seed("Imported", fakeDataResidency{
		WorkspaceGeo:         "eu",
		AllowedInferenceGeos: []string{"eu"},
		DefaultInferenceGeo:  "eu",
	})
	fake.mu.Unlock()

	unmanaged := testProviderConfig + `
resource "anthropic_workspace" "test" {
  name = "Imported"
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkWorkspacesArchived(fake),
		Steps: []resource.TestStep{
			{
				Config:             unmanaged,
				ResourceName:       workspaceAddress,
				ImportState:        true,
				ImportStateId:      importedId,
				ImportStatePersist: true,
			},
			{
				Config:           unmanaged,
				ConfigPlanChecks: expectEmptyPlan(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceAddress, "id", importedId),
					resource.TestCheckResourceAttr(workspaceAddress, "data_residency.workspace_geo", "eu"),
					resource.TestCheckResourceAttr(workspaceAddress, "data_residency.default_inference_geo", "eu"),
					checkFakeAllowedGeos(fake, []string{"eu"}),
				),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name = "Imported"
  data_residency = {
    workspace_geo          = "eu"
    allowed_inference_geos = ["eu"]
  }
}
`,
				ConfigPlanChecks: expectEmptyPlan(),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name = "Imported"
  data_residency = {
    workspace_geo = "us"
  }
}
`,
				ConfigPlanChecks: expectAction(plancheck.ResourceActionDestroyBeforeCreate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceAddress, "data_residency.workspace_geo", "us"),
					resource.TestCheckNoResourceAttr(workspaceAddress, "data_residency.allowed_inference_geos"),
					resource.TestCheckResourceAttr(workspaceAddress, "data_residency.default_inference_geo", "global"),
					checkIdChanged(&importedId),
				),
			},
		},
	})
}

func TestAccWorkspace_ExternalKeyIdIsWriteOnce(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkWorkspacesArchived(fake),
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name = "Encrypted"
}
`,
				Check: resource.TestCheckNoResourceAttr(workspaceAddress, "external_key_id"),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name            = "Encrypted"
  external_key_id = "ekey_first"
}
`,
				ConfigPlanChecks: expectAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceAddress, "id", "wrkspc_0001"),
					resource.TestCheckResourceAttr(workspaceAddress, "external_key_id", "ekey_first"),
				),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name            = "Encrypted"
  external_key_id = "ekey_second"
}
`,
				ExpectError: regexp.MustCompile(`Write-Once Attribute`),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name = "Encrypted"
}
`,
				ExpectError: regexp.MustCompile(`Write-Once Attribute`),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name            = "Encrypted, renamed"
  external_key_id = "ekey_first"
}
`,
				ConfigPlanChecks: expectAction(plancheck.ResourceActionUpdate),
				Check:            resource.TestCheckResourceAttr(workspaceAddress, "external_key_id", "ekey_first"),
			},
		},
	})
}

func TestAccWorkspace_GoneOutOfBand(t *testing.T) {
	config := testProviderConfig + `
resource "anthropic_workspace" "test" {
  name = "Ephemeral"
}
`
	cases := []struct {
		name   string
		remove func(f *fakeAdminAPI, id string)
	}{
		{"archived", (*fakeAdminAPI).archive},
		{"deleted", (*fakeAdminAPI).remove},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeAdminAPI()
			server := setupTestServer(t, fake)
			setupTestClient(t, server)

			var firstId string

			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				CheckDestroy:             checkWorkspacesArchived(fake),
				Steps: []resource.TestStep{
					{
						Config: config,
						Check:  captureId(&firstId),
					},
					{
						PreConfig: func() {
							fake.mu.Lock()
							defer fake.mu.Unlock()
							tc.remove(fake, firstId)
						},
						Config:           config,
						ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate),
						Check:            checkIdChanged(&firstId),
					},
				},
			})
		})
	}
}

func TestAccWorkspace_RejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		expect string
	}{
		{"empty name", `
  name = ""`, `string length must be at least 1`},
		{"display color without hash", `
  name          = "Invalid"
  display_color = "6C5BB9"`, `must be a hex color code`},
		{"display color short form", `
  name          = "Invalid"
  display_color = "#FFF"`, `must be a hex color code`},
		{"empty allowed inference geos", `
  name = "Invalid"
  data_residency = {
    allowed_inference_geos = []
  }`, `set must contain at least 1\s+elements`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := setupTestServer(t, newFakeAdminAPI())
			setupTestClient(t, server)

			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      testProviderConfig + `resource "anthropic_workspace" "test" {` + tc.body + "\n}\n",
						PlanOnly:    true,
						ExpectError: regexp.MustCompile(tc.expect),
					},
				},
			})
		})
	}
}

func TestAccWorkspace_RejectsReservedTagKeys(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
resource "anthropic_workspace" "test" {
  name = "Reserved"
  tags = {
    anthropic_team = "platform"
  }
}
`,
				ExpectError: regexp.MustCompile(`must not begin with\s+.anthropic.`),
			},
		},
	})
}
