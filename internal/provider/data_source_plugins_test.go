package provider

import (
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const pluginsDataSourceAddress = "data.anthropic_plugins.test"

func pluginsDataSourceConfig(filter string) string {
	return testProviderConfig + fmt.Sprintf(`
data "anthropic_plugins" "test" {
  %s
}
`, filter)
}

func TestAccPlugins_Filters(t *testing.T) {
	fake, engineering, linter := newPluginFake(t)
	fake.mu.Lock()
	design := fake.seedPluginMarketplace("design", "", "manual")
	fake.seedPlugin("palette", design, "")
	cutoff := fake.plugins.plugins[linter].CreatedAt.Add(30 * time.Second).Format(time.RFC3339)
	personal := fake.seedPluginMarketplace("personal", "user_0001", "manual")
	fake.seedPlugin("notes", personal, "user_0001")
	fake.mu.Unlock()

	cases := []struct {
		name   string
		filter string
		id     string
		count  string
	}{
		{"unfiltered", "", "plugins", "3"},
		{"by marketplace_id", fmt.Sprintf("marketplace_id = %q", engineering), "plugins/marketplace_id=" + engineering, "1"},
		{"by unknown marketplace_id", `marketplace_id = "marketplace_missing"`, "plugins/marketplace_id=marketplace_missing", "0"},
		{"by owner_type", `owner_type = "organization"`, "plugins/owner_type=organization", "2"},
		{"by owner_user_id", `owner_user_id = "user_0001"`, "plugins/owner_user_id=user_0001", "1"},
		{"by created_at_gt", fmt.Sprintf("created_at_gt = %q", cutoff), "plugins/created_at_gt=" + cutoff, "2"},
		{"by created_at_lte", fmt.Sprintf("created_at_lte = %q", cutoff), "plugins/created_at_lte=" + cutoff, "1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: pluginsDataSourceConfig(tc.filter),
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttr(pluginsDataSourceAddress, "id", tc.id),
							resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.#", tc.count),
						),
					},
				},
			})
		})
	}
}

func TestAccPlugins_Attributes(t *testing.T) {
	fake, marketplaceId, pluginId := newPluginFake(t)
	fake.mu.Lock()
	fake.plugins.marketplaces[marketplaceId].Default = "available"
	personal := fake.seedPluginMarketplace("personal", "user_0001", "manual")
	memberPluginId := fake.seedPlugin("notes", personal, "user_0001")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: pluginsDataSourceConfig(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.#", "2"),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.0.id", memberPluginId),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.0.owner.type", "user"),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.0.owner.user_id", "user_0001"),
					resource.TestCheckNoResourceAttr(pluginsDataSourceAddress, "plugins.0.organization_installation_preference"),
					resource.TestCheckNoResourceAttr(pluginsDataSourceAddress, "plugins.0.organization_installation_preference_inherited"),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.1.id", pluginId),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.1.name", "linter"),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.1.display_name", "Linter"),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.1.marketplace_id", marketplaceId),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.1.owner.type", "organization"),
					resource.TestCheckNoResourceAttr(pluginsDataSourceAddress, "plugins.1.owner.user_id"),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.1.organization_installation_preference", "available"),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.1.organization_installation_preference_inherited", "true"),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.1.created_by.type", "api_actor"),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.1.created_by.api_key_id", "apikey_0001"),
					resource.TestCheckNoResourceAttr(pluginsDataSourceAddress, "plugins.1.created_by.user_id"),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.1.components.#", "1"),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.1.components.0.type", "skill"),
					resource.TestCheckNoResourceAttr(pluginsDataSourceAddress, "plugins.1.content_scan"),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.1.served_version_pinned", "false"),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.1.reach", "contained"),
				),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.setPluginSetting(pluginId, "organization", "required")
				},
				Config: pluginsDataSourceConfig(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.1.organization_installation_preference", "required"),
					resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.1.organization_installation_preference_inherited", "false"),
				),
			},
		},
	})
}

func TestAccPlugins_Pagination(t *testing.T) {
	fake, marketplaceId, _ := newPluginFake(t)
	fake.mu.Lock()
	for i := range 4 {
		fake.seedPlugin(fmt.Sprintf("plugin%d", i), marketplaceId, "")
	}
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: pluginsDataSourceConfig(""),
				Check:  resource.TestCheckResourceAttr(pluginsDataSourceAddress, "plugins.#", "5"),
			},
		},
	})
}

func TestAccPlugins_InvalidTimestamp(t *testing.T) {
	newPluginFake(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      pluginsDataSourceConfig(`created_at_gte = "yesterday"`),
				ExpectError: regexp.MustCompile(`Invalid Timestamp`),
			},
		},
	})
}
