package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const workspaceServiceAccountAddress = "anthropic_workspace_service_account.test"

func checkFakeWorkspaceMembership(fake *fakeAdminAPI, check func(*fakeServiceAccountMembership) error) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		id := s.RootModule().Resources[workspaceServiceAccountAddress].Primary.ID
		fake.mu.Lock()
		defer fake.mu.Unlock()
		member, ok := fake.workspaceMembers[id]
		if !ok {
			return fmt.Errorf("workspace service account membership %s is missing from the fake", id)
		}
		return check(member)
	}
}

func expectWorkspaceServiceAccountAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction(workspaceServiceAccountAddress, action),
		},
	}
}

const testWorkspaceServiceAccountFixtures = `
resource "anthropic_workspace" "test" {
  name = "Membership"
}

resource "anthropic_service_account" "test" {
  name = "github-actions-deploy"
}
`

func TestAccWorkspaceServiceAccount_Basic(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + testWorkspaceServiceAccountFixtures + `
resource "anthropic_workspace_service_account" "test" {
  workspace_id        = anthropic_workspace.test.id
  service_account_id  = anthropic_service_account.test.id
  workspace_role      = "workspace_developer"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceServiceAccountAddress, "id", "wrkspc_0001/svac_0001"),
					resource.TestCheckResourceAttr(workspaceServiceAccountAddress, "workspace_id", "wrkspc_0001"),
					resource.TestCheckResourceAttr(workspaceServiceAccountAddress, "service_account_id", "svac_0001"),
					resource.TestCheckResourceAttr(workspaceServiceAccountAddress, "workspace_role", "workspace_developer"),
					resource.TestCheckResourceAttr(workspaceServiceAccountAddress, "implicit", "false"),
					resource.TestCheckResourceAttrSet(workspaceServiceAccountAddress, "created_by_actor_id"),
					checkFakeWorkspaceMembership(fake, func(m *fakeServiceAccountMembership) error {
						if m.WorkspaceRole != "workspace_developer" {
							return fmt.Errorf("fake membership role = %q, want workspace_developer", m.WorkspaceRole)
						}
						return nil
					}),
				),
			},
			{
				Config: testProviderConfig + testWorkspaceServiceAccountFixtures + `
resource "anthropic_workspace_service_account" "test" {
  workspace_id        = anthropic_workspace.test.id
  service_account_id  = anthropic_service_account.test.id
  workspace_role      = "workspace_developer"
}
`,
				ConfigPlanChecks: expectEmptyWorkspaceServiceAccountPlan(),
			},
			{
				ResourceName:      workspaceServiceAccountAddress,
				ImportState:       true,
				ImportStateIdFunc: workspaceServiceAccountImportId,
				ImportStateVerify: true,
			},
			{
				Config: testProviderConfig + testWorkspaceServiceAccountFixtures + `
resource "anthropic_workspace_service_account" "test" {
  workspace_id        = anthropic_workspace.test.id
  service_account_id  = anthropic_service_account.test.id
  workspace_role      = "workspace_admin"
}
`,
				ConfigPlanChecks: expectWorkspaceServiceAccountAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceServiceAccountAddress, "workspace_role", "workspace_admin"),
					checkFakeWorkspaceMembership(fake, func(m *fakeServiceAccountMembership) error {
						if m.WorkspaceRole != "workspace_admin" {
							return fmt.Errorf("fake membership role = %q, want workspace_admin", m.WorkspaceRole)
						}
						return nil
					}),
				),
			},
		},
	})
}

func expectEmptyWorkspaceServiceAccountPlan() resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}

func workspaceServiceAccountImportId(s *terraform.State) (string, error) {
	rs, ok := s.RootModule().Resources[workspaceServiceAccountAddress]
	if !ok {
		return "", fmt.Errorf("resource not found: %s", workspaceServiceAccountAddress)
	}
	return rs.Primary.Attributes["workspace_id"] + "/" + rs.Primary.Attributes["service_account_id"], nil
}

func TestAccWorkspaceServiceAccount_RejectsInvalidImportId(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + testWorkspaceServiceAccountFixtures + `
resource "anthropic_workspace_service_account" "test" {
  workspace_id        = anthropic_workspace.test.id
  service_account_id  = anthropic_service_account.test.id
  workspace_role      = "workspace_developer"
}
`,
			},
			{
				ResourceName:      workspaceServiceAccountAddress,
				ImportState:       true,
				ImportStateId:     "not-a-valid-id",
				ImportStateVerify: false,
				ExpectError:       regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestAccWorkspaceServiceAccount_RejectsInvalidRole(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + testWorkspaceServiceAccountFixtures + `
resource "anthropic_workspace_service_account" "test" {
  workspace_id        = anthropic_workspace.test.id
  service_account_id  = anthropic_service_account.test.id
  workspace_role      = "workspace_billing"
}
`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
		},
	})
}
