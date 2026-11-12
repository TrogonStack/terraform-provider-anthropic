package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const rbacGroupAddress = "anthropic_rbac_group.test"

func rbacGroupConfig(name string) string {
	return testProviderConfig + fmt.Sprintf(`
resource "anthropic_rbac_group" "test" {
  name = %q
}
`, name)
}

const rbacGroupRemovedConfig = testProviderConfig + `
removed {
  from = anthropic_rbac_group.test

  lifecycle {
    destroy = false
  }
}
`

func expectRBACGroupAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(rbacGroupAddress, action)},
	}
}

func checkFakeRBACGroupName(fake *fakeAdminAPI, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		id := s.RootModule().Resources[rbacGroupAddress].Primary.ID
		fake.mu.Lock()
		defer fake.mu.Unlock()
		group, ok := fake.rbac.groups[id]
		if !ok {
			return fmt.Errorf("RBAC group %s is missing from the fake", id)
		}
		if group.Name != want {
			return fmt.Errorf("fake RBAC group name = %q, want %q", group.Name, want)
		}
		return nil
	}
}

func checkFakeRBACGroupsGone(fake *fakeAdminAPI) resource.TestCheckFunc {
	return func(*terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		for id, group := range fake.rbac.groups {
			if group.SourceType == "direct" {
				return fmt.Errorf("RBAC group %s still exists in the fake after destroy", id)
			}
		}
		return nil
	}
}

func TestAccRBACGroup_Basic(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkFakeRBACGroupsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: rbacGroupConfig("Engineering"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rbacGroupAddress, "id", "rbac_group_0001"),
					resource.TestCheckResourceAttr(rbacGroupAddress, "name", "Engineering"),
					resource.TestCheckResourceAttr(rbacGroupAddress, "source_type", "direct"),
					resource.TestCheckResourceAttr(rbacGroupAddress, "role_ids.#", "0"),
					resource.TestCheckResourceAttr(rbacGroupAddress, "created_at", "2024-10-30T23:58:27Z"),
					resource.TestCheckResourceAttrSet(rbacGroupAddress, "updated_at"),
					checkFakeRBACGroupName(fake, "Engineering"),
				),
			},
			{
				Config:           rbacGroupConfig("Engineering"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				ResourceName:      rbacGroupAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:           rbacGroupConfig("Platform Engineering"),
				ConfigPlanChecks: expectRBACGroupAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rbacGroupAddress, "id", "rbac_group_0001"),
					resource.TestCheckResourceAttr(rbacGroupAddress, "name", "Platform Engineering"),
					resource.TestCheckResourceAttr(rbacGroupAddress, "updated_at", "2024-10-31T00:58:27Z"),
					checkFakeRBACGroupName(fake, "Platform Engineering"),
				),
			},
		},
	})
}

func TestAccRBACGroup_DeletedOutsideTerraform(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkFakeRBACGroupsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: rbacGroupConfig("Engineering"),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.removeRBACGroup("rbac_group_0001")
				},
				Config:           rbacGroupConfig("Engineering"),
				ConfigPlanChecks: expectRBACGroupAction(plancheck.ResourceActionCreate),
				Check:            resource.TestCheckResourceAttr(rbacGroupAddress, "id", "rbac_group_0002"),
			},
		},
	})
}

func TestAccRBACGroup_KeepsRoleIdsWhileRoleDataIsUnavailable(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	id := fake.seedRBACGroup("Engineering", "direct", "rbac_role_0002", "rbac_role_0001")
	fake.mu.Unlock()

	config := rbacGroupConfig("Engineering")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkFakeRBACGroupsGone(fake),
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       rbacGroupAddress,
				ImportState:        true,
				ImportStateId:      id,
				ImportStatePersist: true,
			},
			{
				Config:           config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rbacGroupAddress, "role_ids.#", "2"),
					resource.TestCheckTypeSetElemAttr(rbacGroupAddress, "role_ids.*", "rbac_role_0001"),
					resource.TestCheckTypeSetElemAttr(rbacGroupAddress, "role_ids.*", "rbac_role_0002"),
				),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.rbac.roleDataUnavailable = true
				},
				Config:           rbacGroupConfig("Renamed"),
				ConfigPlanChecks: expectRBACGroupAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rbacGroupAddress, "name", "Renamed"),
					resource.TestCheckResourceAttr(rbacGroupAddress, "role_ids.#", "2"),
				),
			},
		},
	})
}

func TestAccRBACGroup_SCIMGroupIsReadOnly(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	id := fake.seedRBACGroup("Okta Engineering", "scim", "rbac_role_0001")
	fake.mu.Unlock()

	config := rbacGroupConfig("Okta Engineering")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       rbacGroupAddress,
				ImportState:        true,
				ImportStateId:      id,
				ImportStatePersist: true,
			},
			{
				Config:           config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rbacGroupAddress, "source_type", "scim"),
					resource.TestCheckResourceAttr(rbacGroupAddress, "role_ids.#", "1"),
				),
			},
			{
				Config:      rbacGroupConfig("Renamed"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(rbacGroupSCIMSummary),
			},
			{
				Config:      testProviderConfig,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(rbacGroupSCIMSummary),
			},
			{
				Config: rbacGroupRemovedConfig,
				Check: func(*terraform.State) error {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					if group, ok := fake.rbac.groups[id]; !ok || group.Name != "Okta Engineering" {
						return fmt.Errorf("SCIM group %s was changed or deleted", id)
					}
					return nil
				},
			},
		},
	})
}

func TestAccRBACGroup_ReportsAGroupThatBecameSCIM(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: rbacGroupConfig("Engineering"),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.rbac.groups["rbac_group_0001"].SourceType = "scim"
				},
				Config:      rbacGroupConfig("Renamed"),
				ExpectError: regexp.MustCompile(rbacGroupSCIMSummary),
			},
			{
				Config: rbacGroupRemovedConfig,
			},
		},
	})
}
