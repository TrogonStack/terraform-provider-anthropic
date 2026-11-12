package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const workspaceMemberAddress = "anthropic_workspace_member.test"

func workspaceMemberConfig(workspaceId, userId, role string) string {
	return testProviderConfig + fmt.Sprintf(`
resource "anthropic_workspace_member" "test" {
  workspace_id   = %s
  user_id        = %q
  workspace_role = %q
}
`, workspaceId, userId, role)
}

func checkFakeWorkspaceMemberRole(fake *fakeAdminAPI, workspaceId, userId, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		member, ok := fake.workspaceUsers.members[workspaceId+"/"+userId]
		if !ok {
			return fmt.Errorf("workspace member %s/%s is missing from the fake", workspaceId, userId)
		}
		if member.WorkspaceRole != want {
			return fmt.Errorf("fake member role = %q, want %q", member.WorkspaceRole, want)
		}
		return nil
	}
}

func expectWorkspaceMemberAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction(workspaceMemberAddress, action),
		},
	}
}

func TestAccWorkspaceMember_Basic(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	userId := fake.seedOrganizationUser("developer")
	fake.mu.Unlock()

	workspace := `
resource "anthropic_workspace" "test" {
  name = "Membership"
}
`
	config := func(role string) string {
		return workspaceMemberConfig("anthropic_workspace.test.id", userId, role) + workspace
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if len(fake.workspaceUsers.members) != 0 {
				return fmt.Errorf("expected destroy to remove the membership, %d left", len(fake.workspaceUsers.members))
			}
			if _, ok := fake.organizationRole(userId); !ok {
				return fmt.Errorf("destroy removed user %s from the organization", userId)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config("workspace_developer"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceMemberAddress, "id", "wrkspc_0001/"+userId),
					resource.TestCheckResourceAttr(workspaceMemberAddress, "workspace_id", "wrkspc_0001"),
					resource.TestCheckResourceAttr(workspaceMemberAddress, "user_id", userId),
					resource.TestCheckResourceAttr(workspaceMemberAddress, "workspace_role", "workspace_developer"),
					checkFakeWorkspaceMemberRole(fake, "wrkspc_0001", userId, "workspace_developer"),
				),
			},
			{
				Config: config("workspace_developer"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				ResourceName:      workspaceMemberAddress,
				ImportState:       true,
				ImportStateId:     "wrkspc_0001/" + userId,
				ImportStateVerify: true,
			},
			{
				Config:           config("workspace_admin"),
				ConfigPlanChecks: expectWorkspaceMemberAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(workspaceMemberAddress, "workspace_role", "workspace_admin"),
					checkFakeWorkspaceMemberRole(fake, "wrkspc_0001", userId, "workspace_admin"),
				),
			},
		},
	})
}

func TestAccWorkspaceMember_ChangingUserReplaces(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	workspaceId := fake.seed("Membership", fakeDataResidency{})
	first := fake.seedOrganizationUser("user")
	second := fake.seedOrganizationUser("user")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: workspaceMemberConfig(fmt.Sprintf("%q", workspaceId), first, "workspace_user"),
			},
			{
				Config:           workspaceMemberConfig(fmt.Sprintf("%q", workspaceId), second, "workspace_user"),
				ConfigPlanChecks: expectWorkspaceMemberAction(plancheck.ResourceActionDestroyBeforeCreate),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkFakeWorkspaceMemberRole(fake, workspaceId, second, "workspace_user"),
					func(*terraform.State) error {
						fake.mu.Lock()
						defer fake.mu.Unlock()
						if _, ok := fake.workspaceUsers.members[workspaceId+"/"+first]; ok {
							return fmt.Errorf("replacing the member left %s in the workspace", first)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccWorkspaceMember_RejectsInvalidImportId(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        workspaceMemberConfig(`"wrkspc_0001"`, "user_0001", "workspace_user"),
				ResourceName:  workspaceMemberAddress,
				ImportState:   true,
				ImportStateId: "not-a-valid-id",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestAccWorkspaceMember_RemovedOutOfBandIsRecreated(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	workspaceId := fake.seed("Membership", fakeDataResidency{})
	userId := fake.seedOrganizationUser("developer")
	fake.mu.Unlock()

	config := workspaceMemberConfig(fmt.Sprintf("%q", workspaceId), userId, "workspace_developer")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  checkFakeWorkspaceMemberRole(fake, workspaceId, userId, "workspace_developer"),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					delete(fake.workspaceUsers.members, workspaceId+"/"+userId)
				},
				Config:           config,
				ConfigPlanChecks: expectWorkspaceMemberAction(plancheck.ResourceActionCreate),
				Check:            checkFakeWorkspaceMemberRole(fake, workspaceId, userId, "workspace_developer"),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.seedWorkspaceMember(workspaceId, userId, "workspace_user")
				},
				Config:           config,
				ConfigPlanChecks: expectWorkspaceMemberAction(plancheck.ResourceActionUpdate),
				Check:            checkFakeWorkspaceMemberRole(fake, workspaceId, userId, "workspace_developer"),
			},
		},
	})
}

func TestAccWorkspaceMember_DefaultWorkspaceHasNoImplicitMembership(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	defaultWorkspaceId := fake.seedDefaultWorkspace("Default")
	imported := fake.seedOrganizationUser("user")
	added := fake.seedOrganizationUser("user")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        workspaceMemberConfig(fmt.Sprintf("%q", defaultWorkspaceId), imported, "workspace_user"),
				ResourceName:  workspaceMemberAddress,
				ImportState:   true,
				ImportStateId: defaultWorkspaceId + "/" + imported,
				ExpectError:   regexp.MustCompile(`Cannot import non-existent remote object`),
			},
			{
				Config: workspaceMemberConfig(fmt.Sprintf("%q", defaultWorkspaceId), added, "workspace_user"),
				Check:  checkFakeWorkspaceMemberRole(fake, defaultWorkspaceId, added, "workspace_user"),
			},
		},
	})
}

func TestAccWorkspaceMember_CreateRefusesExistingMembership(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	workspaceId := fake.seed("Membership", fakeDataResidency{})
	userId := fake.seedOrganizationUser("developer")
	fake.seedWorkspaceMember(workspaceId, userId, "workspace_admin")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      workspaceMemberConfig(fmt.Sprintf("%q", workspaceId), userId, "workspace_developer"),
				ExpectError: regexp.MustCompile(`already a member`),
			},
		},
	})

	fake.mu.Lock()
	defer fake.mu.Unlock()
	if got := fake.workspaceUsers.members[workspaceId+"/"+userId].WorkspaceRole; got != "workspace_admin" {
		t.Fatalf("existing membership role = %s, want workspace_admin untouched", got)
	}
}

func TestAccWorkspaceMember_CreateRefusesInheritedMembership(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	workspaceId := fake.seed("Membership", fakeDataResidency{})
	userId := fake.seedOrganizationUser("admin")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      workspaceMemberConfig(fmt.Sprintf("%q", workspaceId), userId, "workspace_admin"),
				ExpectError: regexp.MustCompile(`(?s)with\s+role\s+workspace_admin.*inherited`),
			},
		},
	})
}

func TestAccWorkspaceMember_UnknownUserFails(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	workspaceId := fake.seed("Membership", fakeDataResidency{})
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      workspaceMemberConfig(fmt.Sprintf("%q", workspaceId), "user_missing", "workspace_user"),
				ExpectError: regexp.MustCompile(`(?s)Unable to add user to workspace.*User\s+not\s+found`),
			},
		},
	})
}

func TestAccWorkspaceMember_WorkspaceArchivedOutOfBand(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	workspaceId := fake.seed("Membership", fakeDataResidency{})
	userId := fake.seedOrganizationUser("developer")
	fake.mu.Unlock()

	config := workspaceMemberConfig(fmt.Sprintf("%q", workspaceId), userId, "workspace_developer")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  checkFakeWorkspaceMemberRole(fake, workspaceId, userId, "workspace_developer"),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.archive(workspaceId)
				},
				Config:           config,
				ConfigPlanChecks: expectWorkspaceMemberAction(plancheck.ResourceActionCreate),
				ExpectError:      regexp.MustCompile(`(?s)Unable\s+to\s+check\s+for\s+an\s+existing\s+workspace\s+membership.*Workspace\s+is\s+archived`),
			},
			{
				Config: testProviderConfig,
				Check: func(s *terraform.State) error {
					if _, ok := s.RootModule().Resources[workspaceMemberAddress]; ok {
						return fmt.Errorf("expected the workspace membership to be removed from state")
					}
					return nil
				},
			},
		},
	})
}

func TestAccWorkspaceMember_RejectsBillingRole(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      workspaceMemberConfig(`"wrkspc_0001"`, "user_0001", "workspace_billing"),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
		},
	})
}

func TestAccWorkspaceMember_ImportInheritedMembershipFails(t *testing.T) {
	for _, organizationRole := range []string{"admin", "billing"} {
		t.Run(organizationRole, func(t *testing.T) {
			fake := newFakeAdminAPI()
			server := setupTestServer(t, fake)
			setupTestClient(t, server)

			fake.mu.Lock()
			workspaceId := fake.seed("Membership", fakeDataResidency{})
			userId := fake.seedOrganizationUser(organizationRole)
			fake.mu.Unlock()

			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:        workspaceMemberConfig(fmt.Sprintf("%q", workspaceId), userId, "workspace_admin"),
						ResourceName:  workspaceMemberAddress,
						ImportState:   true,
						ImportStateId: workspaceId + "/" + userId,
						ExpectError:   regexp.MustCompile(`Cannot import non-existent remote object`),
					},
				},
			})
		})
	}
}

func TestAccWorkspaceMember_PromotionToAdminLeavesState(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	workspaceId := fake.seed("Membership", fakeDataResidency{})
	userId := fake.seedOrganizationUser("developer")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: workspaceMemberConfig(fmt.Sprintf("%q", workspaceId), userId, "workspace_developer"),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.promoteOrganizationUser(userId, "admin")
				},
				Config: testProviderConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}
