provider "anthropic" {
  alias   = "plugins"
  api_key = var.anthropic_plugins_api_key
}

resource "anthropic_plugin_installation_setting" "linter_organization" {
  provider                = anthropic.plugins
  plugin_id               = "plugin_01Hq3vX8kZcN2mB7pR4tY9wL"
  target                  = "organization"
  installation_preference = "available"
}

resource "anthropic_plugin_installation_setting" "linter_pilot" {
  provider                = anthropic.plugins
  plugin_id               = "plugin_01Hq3vX8kZcN2mB7pR4tY9wL"
  target                  = "rbac_group_01KwZpXvRtYqNmLs8cDf3HgJ"
  installation_preference = "required"
}
