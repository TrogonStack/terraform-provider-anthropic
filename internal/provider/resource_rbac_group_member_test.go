package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const rbacGroupMemberAddress = "anthropic_rbac_group_member.test"

const rbacGroupWithMemberConfig = testProviderConfig + `
resource "anthropic_rbac_group" "test" {
  name = "Engineering"
}

resource "anthropic_rbac_group_member" "test" {
  rbac_group_id = anthropic_rbac_group.test.id
  user_id       = "user_alice"
}
`

func rbacGroupMemberConfig(rbacGroupId, userId string) string {
	return testProviderConfig + fmt.Sprintf(`
resource "anthropic_rbac_group_member" "test" {
  rbac_group_id = %q
  user_id       = %q
}
`, rbacGroupId, userId)
}

const rbacGroupMemberRemovedConfig = testProviderConfig + `
removed {
  from = anthropic_rbac_group_member.test

  lifecycle {
    destroy = false
  }
}
`

func expectRBACGroupMemberAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(rbacGroupMemberAddress, action)},
	}
}

func checkFakeRBACGroupMember(fake *fakeAdminAPI, rbacGroupId, userId string, want bool) resource.TestCheckFunc {
	return func(*terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		if got := fake.rbacGroupMemberIndex(rbacGroupId, userId) >= 0; got != want {
			return fmt.Errorf("fake membership of %s in %s = %t, want %t", userId, rbacGroupId, got, want)
		}
		return nil
	}
}

func newFakeAdminAPIWithRBACUsers(userIds ...string) *fakeAdminAPI {
	fake := newFakeAdminAPI()
	for _, userId := range userIds {
		fake.seedRBACUser(userId, userId+"@example.com")
	}
	return fake
}

func TestAccRBACGroupMember_Basic(t *testing.T) {
	fake := newFakeAdminAPIWithRBACUsers("user_alice")
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkFakeRBACGroupsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: rbacGroupWithMemberConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rbacGroupMemberAddress, "id", "rbac_group_0001/user_alice"),
					resource.TestCheckResourceAttr(rbacGroupMemberAddress, "rbac_group_id", "rbac_group_0001"),
					resource.TestCheckResourceAttr(rbacGroupMemberAddress, "user_id", "user_alice"),
					resource.TestCheckResourceAttr(rbacGroupMemberAddress, "email", "user_alice@example.com"),
					resource.TestCheckResourceAttr(rbacGroupMemberAddress, "created_at", "2024-10-31T01:58:27Z"),
					checkFakeRBACGroupMember(fake, "rbac_group_0001", "user_alice", true),
				),
			},
			{
				Config:           rbacGroupWithMemberConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				ResourceName:      rbacGroupMemberAddress,
				ImportState:       true,
				ImportStateId:     "rbac_group_0001/user_alice",
				ImportStateVerify: true,
			},
			{
				Config: testProviderConfig + `
resource "anthropic_rbac_group" "test" {
  name = "Engineering"
}
`,
				Check: checkFakeRBACGroupMember(fake, "rbac_group_0001", "user_alice", false),
			},
		},
	})
}

func TestAccRBACGroupMember_ReplacesOnUserChange(t *testing.T) {
	fake := newFakeAdminAPIWithRBACUsers("user_alice", "user_bob")
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	rbacGroupId := fake.seedRBACGroup("Engineering", "direct")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: rbacGroupMemberConfig(rbacGroupId, "user_alice"),
			},
			{
				Config:           rbacGroupMemberConfig(rbacGroupId, "user_bob"),
				ConfigPlanChecks: expectRBACGroupMemberAction(plancheck.ResourceActionDestroyBeforeCreate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rbacGroupMemberAddress, "id", rbacGroupId+"/user_bob"),
					checkFakeRBACGroupMember(fake, rbacGroupId, "user_alice", false),
					checkFakeRBACGroupMember(fake, rbacGroupId, "user_bob", true),
				),
			},
		},
	})
}

func TestAccRBACGroupMember_ImportPagesThroughMembers(t *testing.T) {
	userIds := []string{"user_a", "user_b", "user_c", "user_d", "user_e"}
	fake := newFakeAdminAPIWithRBACUsers(userIds...)
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	rbacGroupId := fake.seedRBACGroup("Engineering", "direct")
	for _, userId := range userIds {
		fake.seedRBACGroupMember(rbacGroupId, userId)
	}
	fake.mu.Unlock()

	config := rbacGroupMemberConfig(rbacGroupId, "user_e")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       rbacGroupMemberAddress,
				ImportState:        true,
				ImportStateId:      rbacGroupId + "/user_e",
				ImportStatePersist: true,
			},
			{
				Config:           config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rbacGroupMemberAddress, "id", rbacGroupId+"/user_e"),
					resource.TestCheckResourceAttr(rbacGroupMemberAddress, "email", "user_e@example.com"),
				),
			},
		},
	})
}

func TestAccRBACGroupMember_InvalidImportId(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        rbacGroupMemberConfig("rbac_group_0001", "user_alice"),
				ResourceName:  rbacGroupMemberAddress,
				ImportState:   true,
				ImportStateId: "rbac_group_0001",
				ExpectError:   regexp.MustCompile("Invalid Import ID"),
			},
		},
	})
}

func TestAccRBACGroupMember_ImportOfMissingMembership(t *testing.T) {
	fake := newFakeAdminAPIWithRBACUsers("user_alice")
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	rbacGroupId := fake.seedRBACGroup("Engineering", "direct")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        rbacGroupMemberConfig(rbacGroupId, "user_alice"),
				ResourceName:  rbacGroupMemberAddress,
				ImportState:   true,
				ImportStateId: rbacGroupId + "/user_alice",
				ExpectError:   regexp.MustCompile("Cannot import non-existent remote object"),
			},
		},
	})
}

func TestAccRBACGroupMember_RecreatedAfterDriftOutsideTerraform(t *testing.T) {
	cases := map[string]func(fake *fakeAdminAPI){
		"membership removed": func(fake *fakeAdminAPI) {
			fake.deleteRBACGroupMember("rbac_group_0001", "user_alice")
		},
		"user left the enterprise": func(fake *fakeAdminAPI) {
			delete(fake.rbac.users, "user_alice")
		},
	}

	for name, drift := range cases {
		t.Run(name, func(t *testing.T) {
			fake := newFakeAdminAPIWithRBACUsers("user_alice")
			server := setupTestServer(t, fake)
			setupTestClient(t, server)

			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: rbacGroupWithMemberConfig,
					},
					{
						PreConfig: func() {
							fake.mu.Lock()
							defer fake.mu.Unlock()
							drift(fake)
						},
						Config:             rbacGroupWithMemberConfig,
						PlanOnly:           true,
						ExpectNonEmptyPlan: true,
					},
					{
						PreConfig: func() {
							fake.mu.Lock()
							defer fake.mu.Unlock()
							fake.seedRBACUser("user_alice", "user_alice@example.com")
							fake.deleteRBACGroupMember("rbac_group_0001", "user_alice")
						},
						Config:           rbacGroupWithMemberConfig,
						ConfigPlanChecks: expectRBACGroupMemberAction(plancheck.ResourceActionCreate),
						Check:            checkFakeRBACGroupMember(fake, "rbac_group_0001", "user_alice", true),
					},
				},
			})
		})
	}
}

func TestAccRBACGroupMember_GroupDeletedOutsideTerraform(t *testing.T) {
	fake := newFakeAdminAPIWithRBACUsers("user_alice")
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkFakeRBACGroupsGone(fake),
		Steps: []resource.TestStep{
			{
				Config: rbacGroupWithMemberConfig,
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.removeRBACGroup("rbac_group_0001")
				},
				Config: rbacGroupWithMemberConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(rbacGroupAddress, plancheck.ResourceActionCreate),
						plancheck.ExpectResourceAction(rbacGroupMemberAddress, plancheck.ResourceActionCreate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rbacGroupMemberAddress, "id", "rbac_group_0002/user_alice"),
					checkFakeRBACGroupMember(fake, "rbac_group_0002", "user_alice", true),
				),
			},
		},
	})
}

func TestAccRBACGroupMember_UnknownUser(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	rbacGroupId := fake.seedRBACGroup("Engineering", "direct")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      rbacGroupMemberConfig(rbacGroupId, "user_stranger"),
				ExpectError: regexp.MustCompile("Unable to add user to RBAC group"),
			},
		},
	})
}

func TestAccRBACGroupMember_SCIMGroupRejectsAdd(t *testing.T) {
	fake := newFakeAdminAPIWithRBACUsers("user_alice")
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	rbacGroupId := fake.seedRBACGroup("Okta Engineering", "scim")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      rbacGroupMemberConfig(rbacGroupId, "user_alice"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(rbacGroupSCIMSummary),
			},
		},
	})
}

func TestAccRBACGroupMember_SCIMMembershipIsReadOnly(t *testing.T) {
	fake := newFakeAdminAPIWithRBACUsers("user_alice")
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	rbacGroupId := fake.seedRBACGroup("Okta Engineering", "scim")
	fake.seedRBACGroupMember(rbacGroupId, "user_alice")
	fake.mu.Unlock()

	config := rbacGroupMemberConfig(rbacGroupId, "user_alice")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       rbacGroupMemberAddress,
				ImportState:        true,
				ImportStateId:      rbacGroupId + "/user_alice",
				ImportStatePersist: true,
			},
			{
				Config:           config,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
			{
				Config:      testProviderConfig,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(rbacGroupSCIMSummary),
			},
			{
				Config: rbacGroupMemberRemovedConfig,
				Check:  checkFakeRBACGroupMember(fake, rbacGroupId, "user_alice", true),
			},
		},
	})
}
