package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const userAddress = "anthropic_user.test"

func userConfig(role string) string {
	return testProviderConfig + fmt.Sprintf(`
resource "anthropic_user" "test" {
  role = %q
}
`, role)
}

func checkFakeUser(fake *fakeAdminAPI, id string, check func(*fakeUser) error) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		user, ok := fake.users.members[id]
		if !ok {
			return fmt.Errorf("user %s is missing from the fake", id)
		}
		return check(user)
	}
}

func TestAccUser_CreateFails(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      userConfig("user"),
				ExpectError: regexp.MustCompile(`Users Cannot Be Created`),
			},
		},
	})
}

func TestAccUser_ImportUpdateAndForget(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	userId := fake.seedUser("jane@example.com", "Jane Smith", "user")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             userConfig("user"),
				ResourceName:       userAddress,
				ImportState:        true,
				ImportStateId:      userId,
				ImportStatePersist: true,
			},
			{
				Config: userConfig("user"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(userAddress, "id", userId),
					resource.TestCheckResourceAttr(userAddress, "email", "jane@example.com"),
					resource.TestCheckResourceAttr(userAddress, "name", "Jane Smith"),
					resource.TestCheckResourceAttr(userAddress, "role", "user"),
					resource.TestCheckResourceAttrSet(userAddress, "added_at"),
				),
			},
			{
				Config: userConfig("managed"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(userAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(userAddress, "role", "managed"),
					checkFakeUser(fake, userId, func(u *fakeUser) error {
						if u.Role != "managed" {
							return fmt.Errorf("role = %s, want managed", u.Role)
						}
						return nil
					}),
				),
			},
			{
				Config: testProviderConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					checkFakeUser(fake, userId, func(u *fakeUser) error {
						if u.Role != "managed" {
							return fmt.Errorf("role = %s, want managed (unchanged by destroy)", u.Role)
						}
						return nil
					}),
					func(_ *terraform.State) error {
						fake.mu.Lock()
						defer fake.mu.Unlock()
						if len(fake.users.removedUsers) > 0 {
							return fmt.Errorf("destroy removed members %v, want none", fake.users.removedUsers)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccUser_RoleChangedOutsideTerraform(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	userId := fake.seedUser("jane@example.com", "Jane Smith", "user")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             userConfig("user"),
				ResourceName:       userAddress,
				ImportState:        true,
				ImportStateId:      userId,
				ImportStatePersist: true,
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.users.members[userId].Role = "managed"
				},
				Config: userConfig("user"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(userAddress, plancheck.ResourceActionUpdate)},
				},
				Check: checkFakeUser(fake, userId, func(u *fakeUser) error {
					if u.Role != "user" {
						return fmt.Errorf("role = %s, want user restored", u.Role)
					}
					return nil
				}),
			},
		},
	})
}

func TestAccUser_RemovedOutsideTerraform(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	userId := fake.seedUser("jane@example.com", "Jane Smith", "user")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             userConfig("user"),
				ResourceName:       userAddress,
				ImportState:        true,
				ImportStateId:      userId,
				ImportStatePersist: true,
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					delete(fake.users.members, userId)
				},
				Config:      userConfig("user"),
				ExpectError: regexp.MustCompile(`Users Cannot Be Created`),
			},
		},
	})
}

func TestAccUser_AdministrativeRoleRejectedByAPI(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	userId := fake.seedUser("owner@example.com", "Owner", "owner")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             userConfig("owner"),
				ResourceName:       userAddress,
				ImportState:        true,
				ImportStateId:      userId,
				ImportStatePersist: true,
			},
			{
				Config:      userConfig("user"),
				ExpectError: regexp.MustCompile(`Unable to update user`),
			},
		},
	})
}
