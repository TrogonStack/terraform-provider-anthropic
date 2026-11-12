# Amounts are in the minor unit of the organization's billing currency.
# For USD, "50000" is $500.00.
resource "anthropic_spend_limit" "analyst" {
  user_id = "user_01WCz1FkmYMm4gnmykNKUu3Q"
  period  = "monthly"
  amount  = "50000"
}

# Without an amount, the member has an explicit no-limit override for the
# period instead of the seat tier, group or organization default.
resource "anthropic_spend_limit" "lead" {
  user_id = "user_01Ab6sXyLd3sWnTgPK2bA9zD"
}
