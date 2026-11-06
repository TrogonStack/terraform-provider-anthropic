data "anthropic_rate_limits" "model_groups" {
  group_type = "model_group"
}

output "model_group_rate_limit_ids" {
  value = {
    for rate_limit in data.anthropic_rate_limits.model_groups.rate_limits :
    rate_limit.display_name => rate_limit.id
  }
}
