package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const pluginMarketplaceAddress = "anthropic_plugin_marketplace.test"

const pluginMarketplaceConfig = testProviderConfig + `
resource "anthropic_plugin_marketplace" "test" {}
`

func pluginMarketplaceDefaultConfig(preference string) string {
	return testProviderConfig + fmt.Sprintf(`
resource "anthropic_plugin_marketplace" "test" {
  default_installation_preference = %q
}
`, preference)
}

func checkFakeMarketplaceDefault(fake *fakeAdminAPI, id, want string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		marketplace, ok := fake.plugins.marketplaces[id]
		if !ok {
			return fmt.Errorf("marketplace %s is missing from the fake", id)
		}
		if marketplace.Default != want {
			return fmt.Errorf("default = %q, want %q", marketplace.Default, want)
		}
		return nil
	}
}

func importPluginMarketplace(config, id string) resource.TestStep {
	return resource.TestStep{
		Config:             config,
		ResourceName:       pluginMarketplaceAddress,
		ImportState:        true,
		ImportStateId:      id,
		ImportStatePersist: true,
	}
}

func TestAccPluginMarketplace_CreateFails(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      pluginMarketplaceDefaultConfig("available"),
				ExpectError: regexp.MustCompile(`Plugin Marketplaces Cannot Be Created`),
			},
		},
	})
}

func TestAccPluginMarketplace_Import(t *testing.T) {
	fake, marketplaceId, _ := newPluginFake(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			importPluginMarketplace(pluginMarketplaceConfig, marketplaceId),
			{
				Config:           pluginMarketplaceConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(pluginMarketplaceAddress, "id", marketplaceId),
					resource.TestCheckResourceAttr(pluginMarketplaceAddress, "name", "engineering-tools"),
					resource.TestCheckResourceAttr(pluginMarketplaceAddress, "owner.type", "organization"),
					resource.TestCheckNoResourceAttr(pluginMarketplaceAddress, "owner.user_id"),
					resource.TestCheckResourceAttr(pluginMarketplaceAddress, "source", "github"),
					resource.TestCheckResourceAttr(pluginMarketplaceAddress, "sync_status", "success"),
					resource.TestCheckResourceAttrSet(pluginMarketplaceAddress, "last_sync_ended_at"),
					resource.TestCheckResourceAttrSet(pluginMarketplaceAddress, "last_sync_read_sha"),
					resource.TestCheckResourceAttr(pluginMarketplaceAddress, "default_installation_preference", "not_available"),
					checkFakeMarketplaceDefault(fake, marketplaceId, ""),
				),
			},
			{
				Config: pluginMarketplaceDefaultConfig("available"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(pluginMarketplaceAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(pluginMarketplaceAddress, "default_installation_preference", "available"),
					checkFakeMarketplaceDefault(fake, marketplaceId, "available"),
				),
			},
			{
				Config:           pluginMarketplaceConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check:            checkFakeMarketplaceDefault(fake, marketplaceId, "available"),
			},
			{
				Config: testProviderConfig,
				Check:  checkFakeMarketplaceDefault(fake, marketplaceId, "available"),
			},
		},
	})
}

func TestAccPluginMarketplace_ManualHasNoSync(t *testing.T) {
	fake, _, _ := newPluginFake(t)
	fake.mu.Lock()
	marketplaceId := fake.seedPluginMarketplace("uploads", "", "manual")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			importPluginMarketplace(pluginMarketplaceConfig, marketplaceId),
			{
				Config: pluginMarketplaceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(pluginMarketplaceAddress, "source", "manual"),
					resource.TestCheckNoResourceAttr(pluginMarketplaceAddress, "sync_status"),
					resource.TestCheckNoResourceAttr(pluginMarketplaceAddress, "last_sync_ended_at"),
					resource.TestCheckNoResourceAttr(pluginMarketplaceAddress, "last_sync_read_sha"),
				),
			},
		},
	})
}

func TestAccPluginMarketplace_MemberMarketplaceRejectsUpdate(t *testing.T) {
	fake, _, _ := newPluginFake(t)
	fake.mu.Lock()
	marketplaceId := fake.seedPluginMarketplace("personal", "user_0001", "manual")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			importPluginMarketplace(pluginMarketplaceConfig, marketplaceId),
			{
				Config: pluginMarketplaceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(pluginMarketplaceAddress, "owner.type", "user"),
					resource.TestCheckResourceAttr(pluginMarketplaceAddress, "owner.user_id", "user_0001"),
				),
			},
			{
				Config:      pluginMarketplaceDefaultConfig("available"),
				ExpectError: regexp.MustCompile(`(?s)403\s+Forbidden`),
			},
		},
	})
}

func TestAccPluginMarketplace_RemovedOutsideTerraform(t *testing.T) {
	fake, marketplaceId, _ := newPluginFake(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			importPluginMarketplace(pluginMarketplaceConfig, marketplaceId),
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					delete(fake.plugins.marketplaces, marketplaceId)
				},
				Config:      pluginMarketplaceConfig,
				ExpectError: regexp.MustCompile(`Plugin Marketplaces Cannot Be Created`),
			},
		},
	})
}

func TestAccPluginMarketplace_ImportMissing(t *testing.T) {
	newPluginFake(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:        pluginMarketplaceConfig,
				ResourceName:  pluginMarketplaceAddress,
				ImportState:   true,
				ImportStateId: "marketplace_missing",
				ExpectError:   regexp.MustCompile(`Cannot import non-existent remote object`),
			},
		},
	})
}
