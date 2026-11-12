package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const usersDataSourceAddress = "data.anthropic_users.test"

func TestAccUsers_Filters(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	fake.seedUser("owner@example.com", "Owner", "primary_owner")
	fake.seedUser("admin@example.com", "Admin", "membership_admin")
	fake.seedUser("jane@example.com", "Jane Smith", "user")
	fake.seedUser("sam@example.com", "Sam", "managed")
	newest := fake.seedUser("lee@example.com", "Lee", "user")
	fake.mu.Unlock()

	cases := []struct {
		name   string
		filter string
		id     string
		count  string
	}{
		{"unfiltered", "", "users", "5"},
		{"by email", `email = "Jane@Example.com"`, "users/email=Jane@Example.com", "1"},
		{"by one role", `roles = ["user"]`, "users/roles=user", "2"},
		{"by several roles", `roles = ["primary_owner", "managed", "membership_admin"]`, "users/roles=managed,membership_admin,primary_owner", "3"},
		{"by email and role", `
  email = "sam@example.com"
  roles = ["user"]`, "users/email=sam@example.com/roles=user", "0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: testProviderConfig + fmt.Sprintf(`
data "anthropic_users" "test" {
  %s
}
`, tc.filter),
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttr(usersDataSourceAddress, "id", tc.id),
							resource.TestCheckResourceAttr(usersDataSourceAddress, "users.#", tc.count),
						),
					},
				},
			})
		})
	}

	t.Run("attributes, newest first", func(t *testing.T) {
		resource.Test(t, resource.TestCase{
			ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
			Steps: []resource.TestStep{
				{
					Config: testProviderConfig + `
data "anthropic_users" "test" {}
`,
					Check: resource.ComposeAggregateTestCheckFunc(
						resource.TestCheckResourceAttr(usersDataSourceAddress, "users.0.id", newest),
						resource.TestCheckResourceAttr(usersDataSourceAddress, "users.0.email", "lee@example.com"),
						resource.TestCheckResourceAttr(usersDataSourceAddress, "users.0.name", "Lee"),
						resource.TestCheckResourceAttr(usersDataSourceAddress, "users.0.role", "user"),
						resource.TestCheckResourceAttr(usersDataSourceAddress, "users.0.added_at", "2024-10-31T00:03:27Z"),
						resource.TestCheckResourceAttr(usersDataSourceAddress, "users.4.email", "owner@example.com"),
					),
				},
			},
		})
	})
}
