package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const rbacRolesAddress = "data.anthropic_rbac_roles.test"

const rbacRolesConfig = testProviderConfig + `
data "anthropic_rbac_roles" "test" {}
`

func TestAccRBACRolesDataSource_ListsRolesWithPermissions(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.seedRBACRole("Everything",
		fakeRBACPermission("capability_access_all", "organization", map[string]string{"organization_id": "org_uuid"}),
	)
	fake.seedRBACRole("Connector Users",
		fakeRBACPermission("use", "connector", map[string]string{"connector_id": "connector_github"}),
		fakeRBACPermission("always_allow", "connector_tool", map[string]string{"connector_id": "connector_github", "tool_name": "create_issue"}),
		fakeRBACPermission("grant", "connector_scope", map[string]string{"connector_id": "connector_github", "scope": "repo"}),
		fakeRBACPermission("use", "all_connectors", nil),
	)
	fake.seedRBACRole("Nothing")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: rbacRolesConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rbacRolesAddress, "id", "rbac_roles"),
					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.#", "3"),

					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.0.id", "rbac_role_0001"),
					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.0.display_name", "Everything"),
					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.0.created_at", "2024-10-30T23:58:27Z"),
					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.0.updated_at", "2024-10-31T00:58:27Z"),
					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.0.permissions.#", "1"),
					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.0.permissions.0.action", "capability_access_all"),
					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.0.permissions.0.resource.type", "organization"),
					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.0.permissions.0.resource.organization_id", "org_uuid"),
					resource.TestCheckNoResourceAttr(rbacRolesAddress, "rbac_roles.0.permissions.0.resource.connector_id"),

					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.1.display_name", "Connector Users"),
					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.1.permissions.#", "4"),
					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.1.permissions.0.resource.type", "connector"),
					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.1.permissions.0.resource.connector_id", "connector_github"),
					resource.TestCheckNoResourceAttr(rbacRolesAddress, "rbac_roles.1.permissions.0.resource.tool_name"),
					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.1.permissions.1.action", "always_allow"),
					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.1.permissions.1.resource.tool_name", "create_issue"),
					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.1.permissions.2.resource.scope", "repo"),
					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.1.permissions.3.resource.type", "all_connectors"),
					resource.TestCheckNoResourceAttr(rbacRolesAddress, "rbac_roles.1.permissions.3.resource.organization_id"),
					resource.TestCheckNoResourceAttr(rbacRolesAddress, "rbac_roles.1.permissions.3.resource.connector_id"),

					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.2.display_name", "Nothing"),
					resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.2.permissions.#", "0"),
				),
			},
		},
	})
}

func TestAccRBACRolesDataSource_Empty(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: rbacRolesConfig,
				Check:  resource.TestCheckResourceAttr(rbacRolesAddress, "rbac_roles.#", "0"),
			},
		},
	})
}
