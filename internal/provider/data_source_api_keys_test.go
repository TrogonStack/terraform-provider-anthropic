package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const apiKeysDataSourceAddress = "data.anthropic_api_keys.test"

func TestAccAPIKeys_Filters(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	fake.seedAPIKey(fakeAPIKeySeed{Name: "k1", Status: "active", WorkspaceID: "wrkspc_a", CreatedByID: "user_a", CreatedByType: "user"})
	fake.seedAPIKey(fakeAPIKeySeed{Name: "k2", Status: "inactive", WorkspaceID: "wrkspc_a"})
	fake.seedAPIKey(fakeAPIKeySeed{Name: "k3", Status: "active", WorkspaceID: "wrkspc_b", CreatedByID: "user_b", CreatedByType: "user"})
	fake.seedAPIKey(fakeAPIKeySeed{Name: "k4", Status: "archived", WorkspaceID: "wrkspc_b"})
	fake.mu.Unlock()

	cases := []struct {
		name   string
		filter string
		id     string
		count  string
	}{
		{"unfiltered", "", "api_keys", "4"},
		{"by workspace_id", `workspace_id = "wrkspc_a"`, "api_keys/workspace_id=wrkspc_a", "2"},
		{"by status", `status = "active"`, "api_keys/status=active", "2"},
		{"by created_by_user_id", `created_by_user_id = "user_a"`, "api_keys/created_by_user_id=user_a", "1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: testProviderConfig + fmt.Sprintf(`
data "anthropic_api_keys" "test" {
  %s
}
`, tc.filter),
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttr(apiKeysDataSourceAddress, "id", tc.id),
							resource.TestCheckResourceAttr(apiKeysDataSourceAddress, "api_keys.#", tc.count),
						),
					},
				},
			})
		})
	}
}

func TestAccAPIKeys_Pagination(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	const seeded = 5 // more than fakeAPIKeyPageSize, so ListAutoPaging must follow has_more/last_id across several pages
	fake.mu.Lock()
	for i := 0; i < seeded; i++ {
		fake.seedAPIKey(fakeAPIKeySeed{Name: fmt.Sprintf("k%d", i), Status: "active"})
	}
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
data "anthropic_api_keys" "test" {}
`,
				Check: resource.TestCheckResourceAttr(apiKeysDataSourceAddress, "api_keys.#", "5"),
			},
		},
	})
}
