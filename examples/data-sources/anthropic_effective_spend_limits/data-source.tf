data "anthropic_effective_spend_limits" "monthly" {
  periods = ["monthly"]
}

output "members_on_an_inherited_limit" {
  value = [
    for limit in data.anthropic_effective_spend_limits.monthly.effective_spend_limits :
    limit.actor.email_address if limit.source.type != "user"
  ]
}
