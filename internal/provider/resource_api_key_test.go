package provider

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const apiKeyAddress = "anthropic_api_key.test"

func checkFakeAPIKey(fake *fakeAdminAPI, id string, check func(*fakeAPIKey) error) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		key, ok := fake.apiKeys[id]
		if !ok {
			return fmt.Errorf("API key %s is missing from the fake", id)
		}
		return check(key)
	}
}

func expectEmptyAPIKeyPlan() resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}

func TestAccAPIKey_CreateFails(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
resource "anthropic_api_key" "test" {
  name = "CI"
}
`,
				ExpectError: regexp.MustCompile(`The Admin API cannot create API keys`),
			},
		},
	})
}

func TestAccAPIKey_Import(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	keyId := fake.seedAPIKey(fakeAPIKeySeed{
		Name:          "CI",
		Status:        "active",
		WorkspaceID:   "wrkspc_0001",
		PrincipalType: "service_account",
		PrincipalID:   "svcacct_0001",
		CreatedByID:   "user_0001",
		CreatedByType: "user",
	})
	fake.mu.Unlock()

	importConfig := testProviderConfig + `
resource "anthropic_api_key" "test" {
  name = "CI"
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             importConfig,
				ResourceName:       apiKeyAddress,
				ImportState:        true,
				ImportStateId:      keyId,
				ImportStatePersist: true,
			},
			{
				Config:           importConfig,
				ConfigPlanChecks: expectEmptyAPIKeyPlan(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(apiKeyAddress, "id", keyId),
					resource.TestCheckResourceAttr(apiKeyAddress, "name", "CI"),
					resource.TestCheckResourceAttr(apiKeyAddress, "status", "active"),
					resource.TestCheckNoResourceAttr(apiKeyAddress, "expires_at"),
					resource.TestCheckResourceAttr(apiKeyAddress, "principal.type", "service_account_actor"),
					resource.TestCheckResourceAttr(apiKeyAddress, "principal.service_account_id", "svcacct_0001"),
					resource.TestCheckNoResourceAttr(apiKeyAddress, "principal.user_id"),
					resource.TestCheckResourceAttr(apiKeyAddress, "scope.type", "workspace"),
					resource.TestCheckResourceAttr(apiKeyAddress, "scope.workspace_id", "wrkspc_0001"),
					resource.TestCheckResourceAttr(apiKeyAddress, "created_by.id", "user_0001"),
					resource.TestCheckResourceAttr(apiKeyAddress, "created_by.type", "user"),
				),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_api_key" "test" {
  name = "CI, renamed"
}
`,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(apiKeyAddress, plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr(apiKeyAddress, "name", "CI, renamed"),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_api_key" "test" {
  name   = "CI, renamed"
  status = "inactive"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(apiKeyAddress, "status", "inactive"),
					checkFakeAPIKey(fake, keyId, func(k *fakeAPIKey) error {
						if k.Status != "inactive" {
							return fmt.Errorf("status = %s, want inactive", k.Status)
						}
						return nil
					}),
				),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_api_key" "test" {
  name   = "CI, renamed"
  status = "archived"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(apiKeyAddress, "status", "archived"),
					checkFakeAPIKey(fake, keyId, func(k *fakeAPIKey) error {
						if k.Status != "archived" {
							return fmt.Errorf("status = %s, want archived", k.Status)
						}
						return nil
					}),
				),
			},
			{
				Config: testProviderConfig,
				Check: checkFakeAPIKey(fake, keyId, func(k *fakeAPIKey) error {
					if k.Status != "archived" {
						return fmt.Errorf("status = %s, want archived (unchanged by destroy)", k.Status)
					}
					if k.Name != "CI, renamed" {
						return fmt.Errorf("name = %s, want %q (unchanged by destroy)", k.Name, "CI, renamed")
					}
					return nil
				}),
			},
		},
	})
}

func TestAccAPIKey_ImportOrganizationScopedPrincipalLessNeverExpiring(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	keyId := fake.seedAPIKey(fakeAPIKeySeed{
		Name:   "Legacy",
		Status: "active",
	})
	fake.mu.Unlock()

	config := testProviderConfig + `
resource "anthropic_api_key" "test" {
  name = "Legacy"
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       apiKeyAddress,
				ImportState:        true,
				ImportStateId:      keyId,
				ImportStatePersist: true,
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyAPIKeyPlan(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(apiKeyAddress, "scope.type", "organization"),
					resource.TestCheckNoResourceAttr(apiKeyAddress, "scope.workspace_id"),
					resource.TestCheckNoResourceAttr(apiKeyAddress, "principal.type"),
					resource.TestCheckNoResourceAttr(apiKeyAddress, "created_by.id"),
					resource.TestCheckNoResourceAttr(apiKeyAddress, "expires_at"),
				),
			},
		},
	})
}

func TestFakeRejectsSettingAPIKeyStatusToExpired(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	client, err := newClient(clientConfig{apiKey: testAPIKey, baseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}

	fake.mu.Lock()
	keyId := fake.seedAPIKey(fakeAPIKeySeed{Name: "Expiring"})
	fake.mu.Unlock()

	_, err = client.Organization.APIKeys.Update(t.Context(), keyId, anthropic.OrganizationAPIKeyUpdateParams{
		Status: "expired",
	})
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected setting status to expired to fail with 400, got: %v", err)
	}
}
