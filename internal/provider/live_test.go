package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const liveProviderConfig = `
provider "anthropic" {}
`

func requireLiveCredentials(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("set TF_ACC=1 to run live acceptance tests against a real Anthropic organization")
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" && os.Getenv("ANTHROPIC_AUTH_TOKEN") == "" {
		t.Skip("set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN to run live acceptance tests against a real Anthropic organization")
	}
}

func requireLiveAuthToken(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("set TF_ACC=1 to run live acceptance tests against a real Anthropic organization")
	}
	if os.Getenv("ANTHROPIC_AUTH_TOKEN") == "" {
		t.Skip("set ANTHROPIC_AUTH_TOKEN to run live acceptance tests against service account and federation endpoints; an Admin API key is rejected")
	}
}

func liveAnthropicClient(t *testing.T) *anthropic.Client {
	t.Helper()
	client, err := newClient(clientConfig{})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestLive_Workspace(t *testing.T) {
	requireLiveCredentials(t)

	client := liveAnthropicClient(t)
	name := "tf-live-" + acctest.RandString(8)
	var workspaceId string

	config := func(name, env string) string {
		return liveProviderConfig + fmt.Sprintf(`
resource "anthropic_workspace" "test" {
  name = %q
  tags = {
    env = %q
  }
}
`, name, env)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			return checkLiveWorkspaceArchived(client, workspaceId)
		},
		Steps: []resource.TestStep{
			{
				Config: config(name, "test"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_workspace.test", "id"),
					resource.TestCheckResourceAttr("anthropic_workspace.test", "name", name),
					resource.TestCheckResourceAttrSet("anthropic_workspace.test", "display_color"),
					resource.TestCheckResourceAttr("anthropic_workspace.test", "tags.env", "test"),
					resource.TestCheckResourceAttr("anthropic_workspace.test", "data_residency.workspace_geo", "us"),
					resource.TestCheckResourceAttrSet("anthropic_workspace.test", "compartment_id"),
					resource.TestCheckResourceAttrSet("anthropic_workspace.test", "created_at"),
					func(s *terraform.State) error {
						workspaceId = s.RootModule().Resources["anthropic_workspace.test"].Primary.ID
						return nil
					},
				),
			},
			{
				ResourceName:      "anthropic_workspace.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: config(name+"-renamed", "staging"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_workspace.test", "name", name+"-renamed"),
					resource.TestCheckResourceAttr("anthropic_workspace.test", "tags.%", "1"),
					resource.TestCheckResourceAttr("anthropic_workspace.test", "tags.env", "staging"),
				),
			},
			{
				Config: liveProviderConfig + fmt.Sprintf(`
resource "anthropic_workspace" "test" {
  name = "%s-renamed"
}
`, name),
				Check: resource.TestCheckResourceAttr("anthropic_workspace.test", "tags.%", "0"),
			},
		},
	})
}

func checkLiveWorkspaceArchived(client *anthropic.Client, workspaceId string) error {
	if workspaceId == "" {
		return fmt.Errorf("no workspace ID was captured to verify destruction")
	}
	workspace, err := client.Organization.Workspaces.Get(context.Background(), workspaceId)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read workspace while verifying destroy of %s: %w", workspaceId, err)
	}
	if workspace.ArchivedAt.IsZero() {
		return fmt.Errorf("expected workspace %s to be archived, but it is still active", workspaceId)
	}
	return nil
}

func TestLive_ServiceAccount(t *testing.T) {
	requireLiveAuthToken(t)

	client := liveAnthropicClient(t)
	name := "tf-live-" + strings.ToLower(acctest.RandString(8))
	var serviceAccountId string

	config := func(role string) string {
		return liveProviderConfig + fmt.Sprintf(`
resource "anthropic_workspace" "test" {
  name = %q
}

resource "anthropic_service_account" "test" {
  name = %q
}

resource "anthropic_workspace_service_account" "test" {
  workspace_id       = anthropic_workspace.test.id
  service_account_id = anthropic_service_account.test.id
  workspace_role     = %q
}
`, name, name, role)
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(_ *terraform.State) error {
			return checkLiveServiceAccountArchived(client, serviceAccountId)
		},
		Steps: []resource.TestStep{
			{
				Config: config("workspace_developer"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_service_account.test", "id"),
					resource.TestCheckResourceAttr("anthropic_service_account.test", "name", name),
					resource.TestCheckResourceAttr("anthropic_service_account.test", "organization_role", "developer"),
					resource.TestCheckResourceAttr("anthropic_workspace_service_account.test", "workspace_role", "workspace_developer"),
					func(s *terraform.State) error {
						serviceAccountId = s.RootModule().Resources["anthropic_service_account.test"].Primary.ID
						return nil
					},
				),
			},
			{
				Config: config("workspace_admin"),
				Check:  resource.TestCheckResourceAttr("anthropic_workspace_service_account.test", "workspace_role", "workspace_admin"),
			},
			{
				ResourceName:      "anthropic_service_account.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName: "anthropic_workspace_service_account.test",
				ImportState:  true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					rs := s.RootModule().Resources["anthropic_workspace_service_account.test"]
					return rs.Primary.Attributes["workspace_id"] + "/" + rs.Primary.Attributes["service_account_id"], nil
				},
				ImportStateVerify: true,
			},
		},
	})
}

func checkLiveServiceAccountArchived(client *anthropic.Client, serviceAccountId string) error {
	if serviceAccountId == "" {
		return fmt.Errorf("no service account ID was captured to verify destruction")
	}
	serviceAccount, err := client.Organization.ServiceAccounts.Get(context.Background(), serviceAccountId)
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read service account while verifying destroy of %s: %w", serviceAccountId, err)
	}
	if serviceAccount.ArchivedAt.IsZero() {
		return fmt.Errorf("expected service account %s to be archived, but it is still active", serviceAccountId)
	}
	return nil
}
