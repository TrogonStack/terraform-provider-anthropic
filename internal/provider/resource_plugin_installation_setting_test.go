package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const pluginInstallationSettingAddress = "anthropic_plugin_installation_setting.test"

func pluginInstallationSettingConfig(pluginId, target, preference string) string {
	return testProviderConfig + fmt.Sprintf(`
resource "anthropic_plugin_installation_setting" "test" {
  plugin_id               = %q
  target                  = %q
  installation_preference = %q
}
`, pluginId, target, preference)
}

func checkFakePluginSetting(fake *fakeAdminAPI, pluginId, target, want string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		setting, ok := fake.plugins.plugins[pluginId].settings[target]
		switch {
		case want == "" && ok:
			return fmt.Errorf("target %s still holds %s for plugin %s", target, setting.Preference, pluginId)
		case want == "":
			return nil
		case !ok:
			return fmt.Errorf("target %s holds no setting for plugin %s", target, pluginId)
		case setting.Preference != want:
			return fmt.Errorf("installation_preference = %s, want %s", setting.Preference, want)
		}
		return nil
	}
}

func expectPluginInstallationSettingAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(pluginInstallationSettingAddress, action)},
	}
}

func newPluginFake(t *testing.T) (*fakeAdminAPI, string, string) {
	t.Helper()
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	marketplaceId := fake.seedPluginMarketplace("engineering-tools", "", "github")
	pluginId := fake.seedPlugin("linter", marketplaceId, "")
	return fake, marketplaceId, pluginId
}

func TestAccPluginInstallationSetting_Organization(t *testing.T) {
	fake, _, pluginId := newPluginFake(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: pluginInstallationSettingConfig(pluginId, "organization", "available"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(pluginInstallationSettingAddress, "id", pluginId+"/organization"),
					resource.TestCheckResourceAttr(pluginInstallationSettingAddress, "installation_preference", "available"),
					resource.TestCheckResourceAttrSet(pluginInstallationSettingAddress, "created_at"),
					resource.TestCheckResourceAttrSet(pluginInstallationSettingAddress, "updated_at"),
					checkFakePluginSetting(fake, pluginId, "organization", "available"),
				),
			},
			{
				Config:           pluginInstallationSettingConfig(pluginId, "organization", "required"),
				ConfigPlanChecks: expectPluginInstallationSettingAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(pluginInstallationSettingAddress, "installation_preference", "required"),
					checkFakePluginSetting(fake, pluginId, "organization", "required"),
				),
			},
			{
				ResourceName:      pluginInstallationSettingAddress,
				ImportState:       true,
				ImportStateId:     pluginId + "/organization",
				ImportStateVerify: true,
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.setPluginSetting(pluginId, "organization", "not_available")
				},
				Config:           pluginInstallationSettingConfig(pluginId, "organization", "required"),
				ConfigPlanChecks: expectPluginInstallationSettingAction(plancheck.ResourceActionUpdate),
				Check:            checkFakePluginSetting(fake, pluginId, "organization", "required"),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					delete(fake.plugins.plugins[pluginId].settings, "organization")
				},
				Config:           pluginInstallationSettingConfig(pluginId, "organization", "required"),
				ConfigPlanChecks: expectPluginInstallationSettingAction(plancheck.ResourceActionCreate),
				Check:            checkFakePluginSetting(fake, pluginId, "organization", "required"),
			},
			{
				Config: testProviderConfig,
				Check:  checkFakePluginSetting(fake, pluginId, "organization", ""),
			},
		},
	})
}

func TestAccPluginInstallationSetting_RBACGroup(t *testing.T) {
	fake, _, pluginId := newPluginFake(t)
	fake.mu.Lock()
	fake.seedPluginTargetRBACGroup("rbac_group_pilot")
	fake.setPluginSetting(pluginId, "organization", "not_available")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: pluginInstallationSettingConfig(pluginId, "rbac_group_pilot", "auto_install"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(pluginInstallationSettingAddress, "id", pluginId+"/rbac_group_pilot"),
					resource.TestCheckResourceAttr(pluginInstallationSettingAddress, "target", "rbac_group_pilot"),
					checkFakePluginSetting(fake, pluginId, "rbac_group_pilot", "auto_install"),
				),
			},
			{
				ResourceName:      pluginInstallationSettingAddress,
				ImportState:       true,
				ImportStateId:     pluginId + "/rbac_group_pilot",
				ImportStateVerify: true,
			},
			{
				Config: testProviderConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					checkFakePluginSetting(fake, pluginId, "rbac_group_pilot", ""),
					checkFakePluginSetting(fake, pluginId, "organization", "not_available"),
				),
			},
		},
	})
}

func TestAccPluginInstallationSetting_ChangingTargetReplaces(t *testing.T) {
	fake, _, pluginId := newPluginFake(t)
	fake.mu.Lock()
	fake.seedPluginTargetRBACGroup("rbac_group_pilot")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: pluginInstallationSettingConfig(pluginId, "rbac_group_pilot", "available"),
			},
			{
				Config:           pluginInstallationSettingConfig(pluginId, "organization", "available"),
				ConfigPlanChecks: expectPluginInstallationSettingAction(plancheck.ResourceActionDestroyBeforeCreate),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkFakePluginSetting(fake, pluginId, "rbac_group_pilot", ""),
					checkFakePluginSetting(fake, pluginId, "organization", "available"),
				),
			},
		},
	})
}

func TestAccPluginInstallationSetting_CreateRefusesExistingSetting(t *testing.T) {
	fake, _, pluginId := newPluginFake(t)
	fake.mu.Lock()
	fake.setPluginSetting(pluginId, "organization", "required")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      pluginInstallationSettingConfig(pluginId, "organization", "available"),
				ExpectError: regexp.MustCompile(`Installation Setting Already Exists`),
			},
		},
	})
	if err := checkFakePluginSetting(fake, pluginId, "organization", "required")(nil); err != nil {
		t.Fatal(err)
	}
}

func TestAccPluginInstallationSetting_MemberOwnedPlugin(t *testing.T) {
	fake, marketplaceId, _ := newPluginFake(t)
	fake.mu.Lock()
	pluginId := fake.seedPlugin("personal", marketplaceId, "user_0001")
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      pluginInstallationSettingConfig(pluginId, "organization", "available"),
				ExpectError: regexp.MustCompile(`(?s)404\s+Not\s+Found.*Plugin\s+not\s+found`),
			},
		},
	})
}

func TestAccPluginInstallationSetting_UnknownGroup(t *testing.T) {
	_, _, pluginId := newPluginFake(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      pluginInstallationSettingConfig(pluginId, "rbac_group_missing", "available"),
				ExpectError: regexp.MustCompile(`(?s)404\s+Not\s+Found.*RBAC\s+group\s+not\s+found`),
			},
		},
	})
}

func TestAccPluginInstallationSetting_InvalidTarget(t *testing.T) {
	_, _, pluginId := newPluginFake(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      pluginInstallationSettingConfig(pluginId, "user_0001", "available"),
				ExpectError: regexp.MustCompile(`Invalid Attribute Value Match`),
			},
		},
	})
}

func TestAccPluginInstallationSetting_InvalidImportID(t *testing.T) {
	_, _, pluginId := newPluginFake(t)

	for _, id := range []string{pluginId, pluginId + "/user_0001", "/organization"} {
		t.Run(id, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:        pluginInstallationSettingConfig(pluginId, "organization", "available"),
						ResourceName:  pluginInstallationSettingAddress,
						ImportState:   true,
						ImportStateId: id,
						ExpectError:   regexp.MustCompile(`Invalid Import ID`),
					},
				},
			})
		})
	}
}

func TestAccPluginInstallationSetting_ReadPaginates(t *testing.T) {
	fake, _, pluginId := newPluginFake(t)
	fake.mu.Lock()
	for _, group := range []string{"rbac_group_a", "rbac_group_b", "rbac_group_c", "rbac_group_d"} {
		fake.seedPluginTargetRBACGroup(group)
		fake.setPluginSetting(pluginId, group, "available")
	}
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             pluginInstallationSettingConfig(pluginId, "rbac_group_a", "available"),
				ResourceName:       pluginInstallationSettingAddress,
				ImportState:        true,
				ImportStateId:      pluginId + "/rbac_group_a",
				ImportStatePersist: true,
			},
			{
				Config:           pluginInstallationSettingConfig(pluginId, "rbac_group_a", "available"),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check:            resource.TestCheckResourceAttr(pluginInstallationSettingAddress, "id", pluginId+"/rbac_group_a"),
			},
		},
	})
}

func TestFakePluginsRequireBeta(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())

	for beta, want := range map[string]int{"": http.StatusNotFound, fakePluginsBeta: http.StatusOK} {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/v1/organizations/plugins", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Api-Key", testAPIKey)
		if beta != "" {
			req.Header.Set("anthropic-beta", beta)
		}
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("anthropic-beta %q: status = %d, want %d", beta, resp.StatusCode, want)
		}
	}
}
