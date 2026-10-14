package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const telemetryDestinationsDataSourceAddress = "data.anthropic_telemetry_destinations.test"

func TestAccTelemetryDestinations_Filter(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)
	ids := seedWorkspaces(fake, 2)

	fake.mu.Lock()
	fake.seedTelemetryDestination(fakeTelemetryDestinationSeed{WorkspacesType: "all"})
	fake.seedTelemetryDestination(fakeTelemetryDestinationSeed{WorkspacesType: "include", WorkspaceIDs: []string{ids[0]}})
	fake.seedTelemetryDestination(fakeTelemetryDestinationSeed{WorkspacesType: "exclude", WorkspaceIDs: []string{ids[0]}})
	fake.seedTelemetryDestination(fakeTelemetryDestinationSeed{WorkspacesType: "include", WorkspaceIDs: []string{ids[1]}})
	fake.mu.Unlock()

	cases := []struct {
		name   string
		filter string
		id     string
		count  string
	}{
		{"unfiltered", "", "telemetry_destinations", "4"},
		{"first workspace", fmt.Sprintf("workspace_ids = [%q]", ids[0]), "telemetry_destinations/workspace_ids=" + ids[0], "2"},
		{"second workspace", fmt.Sprintf("workspace_ids = [%q]", ids[1]), "telemetry_destinations/workspace_ids=" + ids[1], "3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				Steps: []resource.TestStep{
					{
						Config: testProviderConfig + fmt.Sprintf(`
data "anthropic_telemetry_destinations" "test" {
  %s
}
`, tc.filter),
						Check: resource.ComposeAggregateTestCheckFunc(
							resource.TestCheckResourceAttr(telemetryDestinationsDataSourceAddress, "id", tc.id),
							resource.TestCheckResourceAttr(telemetryDestinationsDataSourceAddress, "telemetry_destinations.#", tc.count),
						),
					},
				},
			})
		})
	}
}

func TestAccTelemetryDestinations_Pagination(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)
	ids := seedWorkspaces(fake, 5)

	fake.mu.Lock()
	for range fakeTelemetryMaxDestinations {
		fake.seedTelemetryDestination(fakeTelemetryDestinationSeed{
			EndpointURL:    "https://seeded.example.com",
			WorkspacesType: "include",
			WorkspaceIDs:   ids,
			Headers:        map[string]string{"authorization": "secret"},
		})
	}
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
data "anthropic_telemetry_destinations" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(telemetryDestinationsDataSourceAddress, "telemetry_destinations.#", "10"),
					resource.TestCheckResourceAttr(telemetryDestinationsDataSourceAddress, "telemetry_destinations.0.id", "tdst_0010"),
					resource.TestCheckResourceAttr(telemetryDestinationsDataSourceAddress, "telemetry_destinations.0.workspaces.workspace_ids.#", "5"),
					resource.TestCheckResourceAttr(telemetryDestinationsDataSourceAddress, "telemetry_destinations.0.header_names.#", "1"),
					resource.TestCheckResourceAttr(telemetryDestinationsDataSourceAddress, "telemetry_destinations.9.id", "tdst_0001"),
				),
			},
		},
	})
}
