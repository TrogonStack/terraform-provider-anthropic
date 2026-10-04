package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const serviceAccountAddress = "anthropic_service_account.test"

func checkFakeServiceAccountsArchived(fake *fakeAdminAPI) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		for id, sa := range fake.serviceAccounts {
			if sa.ArchivedAt == nil {
				return fmt.Errorf("expected service account %s to be archived on destroy", id)
			}
		}
		return nil
	}
}

func checkFakeServiceAccount(fake *fakeAdminAPI, check func(*fakeServiceAccount) error) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		id := s.RootModule().Resources[serviceAccountAddress].Primary.ID
		fake.mu.Lock()
		defer fake.mu.Unlock()
		sa, ok := fake.serviceAccounts[id]
		if !ok {
			return fmt.Errorf("service account %s is missing from the fake", id)
		}
		return check(sa)
	}
}

func captureServiceAccountId(id *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		*id = s.RootModule().Resources[serviceAccountAddress].Primary.ID
		return nil
	}
}

func checkServiceAccountIdChanged(previous *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		if id := s.RootModule().Resources[serviceAccountAddress].Primary.ID; id == *previous {
			return fmt.Errorf("expected a new service account, still %s", id)
		}
		return nil
	}
}

func expectServiceAccountAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{
			plancheck.ExpectResourceAction(serviceAccountAddress, action),
		},
	}
}

func TestAccServiceAccount_Basic(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkFakeServiceAccountsArchived(fake),
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
resource "anthropic_service_account" "test" {
  name = "github-actions-deploy"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(serviceAccountAddress, "id", "svac_0001"),
					resource.TestCheckResourceAttr(serviceAccountAddress, "name", "github-actions-deploy"),
					resource.TestCheckNoResourceAttr(serviceAccountAddress, "description"),
					resource.TestCheckResourceAttr(serviceAccountAddress, "organization_role", "developer"),
					resource.TestCheckResourceAttr(serviceAccountAddress, "created_at", "2024-10-30T23:58:27Z"),
					resource.TestCheckResourceAttrSet(serviceAccountAddress, "created_by_actor_id"),
					resource.TestCheckResourceAttrSet(serviceAccountAddress, "updated_by_actor_id"),
				),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_service_account" "test" {
  name = "github-actions-deploy"
}
`,
				ConfigPlanChecks: expectEmptyServiceAccountPlan(),
			},
			{
				ResourceName:      serviceAccountAddress,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: testProviderConfig + `
resource "anthropic_service_account" "test" {
  name              = "github-actions-deploy"
  description       = "Deploys from GitHub Actions"
  organization_role = "admin"
}
`,
				ConfigPlanChecks: expectServiceAccountAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(serviceAccountAddress, "description", "Deploys from GitHub Actions"),
					resource.TestCheckResourceAttr(serviceAccountAddress, "organization_role", "admin"),
					checkFakeServiceAccount(fake, func(sa *fakeServiceAccount) error {
						if sa.Description != "Deploys from GitHub Actions" || sa.OrganizationRole != "admin" {
							return fmt.Errorf("fake service account = %+v, want updated description and role", sa)
						}
						return nil
					}),
				),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_service_account" "test" {
  name              = "github-actions-deploy"
  organization_role = "admin"
}
`,
				ConfigPlanChecks: expectServiceAccountAction(plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(serviceAccountAddress, "description"),
					checkFakeServiceAccount(fake, func(sa *fakeServiceAccount) error {
						if sa.Description != "" {
							return fmt.Errorf("fake service account description = %q, want empty", sa.Description)
						}
						return nil
					}),
				),
			},
		},
	})
}

func expectEmptyServiceAccountPlan() resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}

func TestAccServiceAccount_NameChangeReplaces(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	var firstId string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkFakeServiceAccountsArchived(fake),
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
resource "anthropic_service_account" "test" {
  name = "first-name"
}
`,
				Check: captureServiceAccountId(&firstId),
			},
			{
				Config: testProviderConfig + `
resource "anthropic_service_account" "test" {
  name = "second-name"
}
`,
				ConfigPlanChecks: expectServiceAccountAction(plancheck.ResourceActionDestroyBeforeCreate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(serviceAccountAddress, "name", "second-name"),
					checkServiceAccountIdChanged(&firstId),
				),
			},
		},
	})
}

func TestAccServiceAccount_GoneOutOfBand(t *testing.T) {
	config := testProviderConfig + `
resource "anthropic_service_account" "test" {
  name = "ephemeral-identity"
}
`
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	var firstId string

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkFakeServiceAccountsArchived(fake),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check:  captureServiceAccountId(&firstId),
			},
			{
				PreConfig: func() {
					fake.mu.Lock()
					defer fake.mu.Unlock()
					fake.archiveServiceAccount(firstId)
				},
				Config:           config,
				ConfigPlanChecks: expectServiceAccountAction(plancheck.ResourceActionCreate),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkServiceAccountIdChanged(&firstId),
					func(s *terraform.State) error {
						fake.mu.Lock()
						defer fake.mu.Unlock()
						if _, ok := fake.serviceAccounts[s.RootModule().Resources[serviceAccountAddress].Primary.ID]; !ok {
							return fmt.Errorf("expected the fake to hold a newly created service account")
						}
						return nil
					},
				),
			},
		},
	})
}

func TestAccServiceAccount_ImportUnmanaged(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	importedId := fake.seedServiceAccount("imported-identity")
	fake.mu.Unlock()

	config := testProviderConfig + `
resource "anthropic_service_account" "test" {
  name = "imported-identity"
}
`

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             checkFakeServiceAccountsArchived(fake),
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       serviceAccountAddress,
				ImportState:        true,
				ImportStateId:      importedId,
				ImportStatePersist: true,
			},
			{
				Config:           config,
				ConfigPlanChecks: expectEmptyServiceAccountPlan(),
				Check:            resource.TestCheckResourceAttr(serviceAccountAddress, "id", importedId),
			},
		},
	})
}

func TestAccServiceAccount_RejectsInvalidConfig(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		expect string
	}{
		{"empty name", `
  name = ""`, `string length must be between 1 and 255`},
		{"uppercase name", `
  name = "Invalid-Name"`, `must contain only lowercase letters, digits, and hyphens`},
		{"empty description", `
  name        = "empty-description"
  description = ""`, `string length must be at least 1`},
		{"invalid organization role", `
  name              = "invalid-role"
  organization_role = "owner"`, `value must be one of`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := setupTestServer(t, newFakeAdminAPI())
			setupTestClient(t, server)

			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config:      testProviderConfig + `resource "anthropic_service_account" "test" {` + tc.body + "\n}\n",
						PlanOnly:    true,
						ExpectError: regexp.MustCompile(tc.expect),
					},
				},
			})
		})
	}
}
