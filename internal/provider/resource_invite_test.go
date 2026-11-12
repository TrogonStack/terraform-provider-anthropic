package provider

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const inviteAddress = "anthropic_invite.test"

func inviteConfig(email, role, groups string) string {
	groupsLine := ""
	if groups != "" {
		groupsLine = "rbac_group_ids = " + groups
	}
	return testProviderConfig + fmt.Sprintf(`
resource "anthropic_invite" "test" {
  email = %q
  role  = %q
  %s
}
`, email, role, groupsLine)
}

func inviteIdFromState(s *terraform.State) string {
	return s.RootModule().Resources[inviteAddress].Primary.ID
}

func checkFakeInviteStatus(fake *fakeAdminAPI, id *string, want string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		invite, ok := fake.users.invites[*id]
		if !ok {
			return fmt.Errorf("invite %s is missing from the fake", *id)
		}
		if invite.Status != want {
			return fmt.Errorf("invite %s status = %s, want %s", *id, invite.Status, want)
		}
		return nil
	}
}

func captureInviteId(id *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		*id = inviteIdFromState(s)
		return nil
	}
}

func expectInviteReplaced() resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(inviteAddress, plancheck.ResourceActionDestroyBeforeCreate)},
	}
}

func TestAccInvite_Lifecycle(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	var firstId, secondId, thirdId string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(s *terraform.State) error {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			for id, invite := range fake.users.invites {
				if invite.Status != "deleted" {
					return fmt.Errorf("invite %s status = %s after destroy, want deleted", id, invite.Status)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: inviteConfig("newhire@example.com", "user", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(inviteAddress, "email", "newhire@example.com"),
					resource.TestCheckResourceAttr(inviteAddress, "role", "user"),
					resource.TestCheckResourceAttr(inviteAddress, "rbac_group_ids.#", "0"),
					resource.TestCheckResourceAttr(inviteAddress, "status", "pending"),
					resource.TestCheckResourceAttr(inviteAddress, "invited_at", "2024-10-30T23:59:27Z"),
					resource.TestCheckResourceAttr(inviteAddress, "expires_at", "2024-11-20T23:59:27Z"),
					resource.TestCheckNoResourceAttr(inviteAddress, "accepted_at"),
					captureInviteId(&firstId),
					func(_ *terraform.State) error {
						fake.mu.Lock()
						defer fake.mu.Unlock()
						if body := fake.users.inviteRequests[0]; strings.Contains(body, "rbac_group_ids") {
							return fmt.Errorf("create sent %s, want no rbac_group_ids", body)
						}
						return nil
					},
				),
			},
			{
				ResourceName:      inviteAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:           inviteConfig("newhire@example.com", "managed", ""),
				ConfigPlanChecks: expectInviteReplaced(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(inviteAddress, "role", "managed"),
					captureInviteId(&secondId),
					checkFakeInviteStatus(fake, &firstId, "deleted"),
					checkFakeInviteStatus(fake, &secondId, "pending"),
				),
			},
			{
				Config:           inviteConfig("newhire@example.com", "managed", `["rbac_group_01", "rbac_group_02"]`),
				ConfigPlanChecks: expectInviteReplaced(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(inviteAddress, "rbac_group_ids.#", "2"),
					resource.TestCheckTypeSetElemAttr(inviteAddress, "rbac_group_ids.*", "rbac_group_01"),
					resource.TestCheckTypeSetElemAttr(inviteAddress, "rbac_group_ids.*", "rbac_group_02"),
					captureInviteId(&thirdId),
					checkFakeInviteStatus(fake, &secondId, "deleted"),
					func(_ *terraform.State) error {
						fake.mu.Lock()
						defer fake.mu.Unlock()
						groups := fake.users.invites[thirdId].RBACGroupIDs
						if len(groups) != 2 {
							return fmt.Errorf("fake invite groups = %v, want two", groups)
						}
						return nil
					},
				),
			},
			{
				Config:           inviteConfig("other@example.com", "managed", `["rbac_group_01", "rbac_group_02"]`),
				ConfigPlanChecks: expectInviteReplaced(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(inviteAddress, "email", "other@example.com"),
					checkFakeInviteStatus(fake, &thirdId, "deleted"),
				),
			},
		},
	})
}

func TestAccInvite_MixedCaseEmailPlansNoChange(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	config := inviteConfig("New.Hire@Example.com", "user", "")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  resource.TestCheckResourceAttr(inviteAddress, "email", "New.Hire@Example.com"),
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
}

func TestAccInvite_AcceptedIsKeptAndNotWithdrawn(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	var inviteId string
	config := inviteConfig("newhire@example.com", "user", "")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkFakeInviteStatus(fake, &inviteId, "accepted"),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  captureInviteId(&inviteId),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.setInviteStatus(inviteId, "accepted")
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPtr(inviteAddress, "id", &inviteId),
					resource.TestCheckResourceAttr(inviteAddress, "status", "accepted"),
					resource.TestCheckResourceAttr(inviteAddress, "accepted_at", "2024-10-31T00:58:27Z"),
				),
			},
		},
	})
}

func TestAccInvite_ExpiredIsSentAgain(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	var firstId, secondId string
	config := inviteConfig("newhire@example.com", "user", "")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  captureInviteId(&firstId),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.setInviteStatus(firstId, "expired")
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(inviteAddress, plancheck.ResourceActionCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					captureInviteId(&secondId),
					resource.TestCheckResourceAttr(inviteAddress, "status", "pending"),
					checkFakeInviteStatus(fake, &firstId, "expired"),
					func(_ *terraform.State) error {
						if secondId == firstId {
							return fmt.Errorf("invite id %s was reused, want a new invite", secondId)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccInvite_WithdrawnOutsideTerraformIsSentAgain(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	var firstId, secondId string
	config := inviteConfig("newhire@example.com", "user", "")

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  captureInviteId(&firstId),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					delete(fake.users.invites, firstId)
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(inviteAddress, plancheck.ResourceActionCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					captureInviteId(&secondId),
					checkFakeInviteStatus(fake, &secondId, "pending"),
				),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.users.invites[secondId].Status = "deleted"
				},
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(inviteAddress, plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}
