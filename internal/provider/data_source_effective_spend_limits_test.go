package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const effectiveSpendLimitsAddress = "data.anthropic_effective_spend_limits.test"

func TestAccEffectiveSpendLimitsDataSource_ListsEveryMemberAcrossPages(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
data "anthropic_effective_spend_limits" "test" {}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "id", "effective_spend_limits"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.#", "8"),

					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.0.actor.type", "user_actor"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.0.actor.user_id", fakeSpendLimitMemberAda),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.0.actor.name", "Ada"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.0.actor.email_address", "ada@example.com"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.0.actor.deleted", "false"),
					resource.TestCheckNoResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.0.actor.scoped_api_key_id"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.0.period", "weekly"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.0.amount", "7500"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.0.source.type", "organization"),

					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.1.period", "monthly"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.1.amount", "50000"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.1.currency", "USD"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.1.period_to_date_spend", "1250.5"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.1.scope.type", "user"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.1.scope.user_id", fakeSpendLimitMemberAda),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.1.source.type", "seat_tier"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.1.source.seat_tier", "premium"),
					resource.TestCheckNoResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.1.source.user_id"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.1.spend_limit_id", "spl_example_premium_monthly"),

					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.3.actor.user_id", fakeSpendLimitMemberDeleted),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.3.actor.deleted", "true"),
					resource.TestCheckNoResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.3.actor.name"),
					resource.TestCheckNoResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.3.actor.email_address"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.3.amount", "20000"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.3.source.type", "organization"),

					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.7.actor.user_id", fakeSpendLimitMemberKatie),
					resource.TestCheckNoResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.7.actor.name"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.7.actor.email_address", "katie@example.com"),
				),
			},
		},
	})
}

func TestAccEffectiveSpendLimitsDataSource_FiltersByUserAndPeriod(t *testing.T) {
	fake := newFakeAdminAPI()
	server := setupTestServer(t, fake)
	setupTestClient(t, server)

	fake.mu.Lock()
	grace := fake.setSpendLimitOutOfBand(fakeSpendLimitMemberGrace, "monthly", fakeStringPointer("30000")).ID
	fake.setSpendLimitOutOfBand(fakeSpendLimitMemberKatie, "monthly", nil)
	fake.mu.Unlock()

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
data "anthropic_effective_spend_limits" "test" {
  user_ids = ["` + fakeSpendLimitMemberKatie + `", "` + fakeSpendLimitMemberGrace + `", "` + fakeSpendLimitMemberAda + `"]
  periods  = ["monthly"]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "id",
						"effective_spend_limits/user_ids="+fakeSpendLimitMemberAda+","+fakeSpendLimitMemberGrace+","+fakeSpendLimitMemberKatie+";periods=monthly"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.#", "3"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.0.actor.user_id", fakeSpendLimitMemberAda),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.0.source.type", "seat_tier"),

					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.1.actor.user_id", fakeSpendLimitMemberGrace),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.1.amount", "30000"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.1.source.type", "user"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.1.source.user_id", fakeSpendLimitMemberGrace),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.1.spend_limit_id", grace),

					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.2.actor.user_id", fakeSpendLimitMemberKatie),
					resource.TestCheckNoResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.2.amount"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.2.source.type", "user"),
				),
			},
		},
	})
}

func TestAccEffectiveSpendLimitsDataSource_ReturnsNothingForAPeriodWithoutLimits(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
data "anthropic_effective_spend_limits" "test" {
  periods = ["daily"]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "id", "effective_spend_limits/periods=daily"),
					resource.TestCheckResourceAttr(effectiveSpendLimitsAddress, "effective_spend_limits.#", "0"),
				),
			},
		},
	})
}

func TestAccEffectiveSpendLimitsDataSource_RejectsTooManyPeriods(t *testing.T) {
	server := setupTestServer(t, newFakeAdminAPI())
	setupTestClient(t, server)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testProviderConfig + `
data "anthropic_effective_spend_limits" "test" {
  periods = ["daily", "weekly", "monthly", "yearly"]
}
`,
				ExpectError: regexp.MustCompile(`set must contain at least 1 elements and at most 3`),
			},
		},
	})
}
