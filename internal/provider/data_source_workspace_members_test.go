package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const workspaceMembersDataSourceAddress = "data.anthropic_workspace_members.test"

func TestAccWorkspaceMembers_ListsEveryPage(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	workspaceId := fake.seed("Membership", fakeDataResidency{})
	otherWorkspaceId := fake.seed("Other", fakeDataResidency{})
	admin := fake.seedOrganizationUser("admin")
	var developers []string
	for range 4 {
		userId := fake.seedOrganizationUser("developer")
		fake.seedWorkspaceMember(workspaceId, userId, "workspace_developer")
		developers = append(developers, userId)
	}
	fake.seedWorkspaceMember(otherWorkspaceId, fake.seedOrganizationUser("user"), "workspace_user")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + fmt.Sprintf(`
data "anthropic_workspace_members" "test" {
  workspace_id = %q
}
`, workspaceId),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceMembersDataSourceAddress, "id", workspaceId),
					resource.TestCheckResourceAttr(workspaceMembersDataSourceAddress, "members.#", "5"),
					resource.TestCheckTypeSetElemNestedAttrs(workspaceMembersDataSourceAddress, "members.*", map[string]string{
						"user_id":        admin,
						"workspace_role": "workspace_admin",
					}),
					resource.TestCheckTypeSetElemNestedAttrs(workspaceMembersDataSourceAddress, "members.*", map[string]string{
						"user_id":        developers[3],
						"workspace_role": "workspace_developer",
					}),
				),
			},
		},
	})
}

func TestAccWorkspaceMembers_ArchivedWorkspaceFails(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	workspaceId := fake.seed("Archived", fakeDataResidency{})
	fake.archive(workspaceId)
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + fmt.Sprintf(`
data "anthropic_workspace_members" "test" {
  workspace_id = %q
}
`, workspaceId),
				ExpectError: regexp.MustCompile(`(?s)Unable to list workspace members.*Workspace\s+is\s+archived`),
			},
		},
	})
}
