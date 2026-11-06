package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const rateLimitsAddress = "data.anthropic_rate_limits.test"

func TestAccRateLimitsDataSource_ListsEveryGroupAcrossPages(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
data "anthropic_rate_limits" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rateLimitsAddress, "id", "rate_limits"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.#", "5"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.0.id", fakeOpusRateLimitID),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.0.group_id", "rlg_01Hq7YkP3mZ9dTwRx4cVbN2s"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.0.group_type", "model_group"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.0.display_name", "Claude Opus 5.5"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.0.limits.requests_per_minute", "4000"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.4.id", fakeWebSearchRateLimitID),
				),
			},
		},
	})
}

func TestAccRateLimitsDataSource_FiltersByModel(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
data "anthropic_rate_limits" "test" {
  model = "claude-haiku-4-5"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rateLimitsAddress, "id", "rate_limits/model=claude-haiku-4-5"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.#", "1"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.0.id", fakeHaikuRateLimitID),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.0.display_name", "Claude Haiku 4.x"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.0.models.#", "2"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.0.models.0", "claude-haiku-4-5"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.0.limits.%", "4"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.0.limits.tokens_per_day", "100000000"),
				),
			},
		},
	})
}

func TestAccRateLimitsDataSource_FiltersByGroupType(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
data "anthropic_rate_limits" "test" {
  group_type = "batch"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rateLimitsAddress, "id", "rate_limits/group_type=batch"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.#", "1"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.0.id", fakeBatchRateLimitID),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.0.group_type", "batch"),
					resource.TestCheckNoResourceAttr(rateLimitsAddress, "rate_limits.0.display_name"),
					resource.TestCheckNoResourceAttr(rateLimitsAddress, "rate_limits.0.models.#"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.0.limits.enqueued_batch_requests", "500000"),
				),
			},
			{
				Config: testProviderConfig + `
data "anthropic_rate_limits" "test" {
  group_type = "model_group"
  model      = "claude-opus-4-7"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rateLimitsAddress, "id", "rate_limits/group_type=model_group,model=claude-opus-4-7"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.#", "1"),
					resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.0.id", fakeOpusFourRateLimitID),
				),
			},
			{
				Config: testProviderConfig + `
data "anthropic_rate_limits" "test" {
  group_type = "files"
}
`,
				Check: resource.TestCheckResourceAttr(rateLimitsAddress, "rate_limits.#", "0"),
			},
		},
	})
}

func TestAccRateLimitsDataSource_UnknownModelFails(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
data "anthropic_rate_limits" "test" {
  model = "claude-example-0"
}
`,
				ExpectError: regexp.MustCompile(`(?s)Model Not Found.*claude-example-0`),
			},
		},
	})
}
