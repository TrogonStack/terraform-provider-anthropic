resource "anthropic_workspace" "production" {
  name = "Production"
}

data "anthropic_rate_limits" "haiku" {
  model = "claude-haiku-4-5"
}

resource "anthropic_workspace_rate_limit" "production_haiku" {
  workspace_id  = anthropic_workspace.production.id
  rate_limit_id = data.anthropic_rate_limits.haiku.rate_limits[0].id

  limits = {
    tokens_per_day      = 5000000
    requests_per_minute = 500
  }
}
