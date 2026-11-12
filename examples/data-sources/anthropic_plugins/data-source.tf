data "anthropic_plugins" "engineering_tools" {
  provider       = anthropic.plugins
  marketplace_id = "marketplace_01VbNcMxZaSdFgHjKlQwErTy"
  owner_type     = "organization"
}

output "plugins_inheriting_the_marketplace_default" {
  value = [
    for plugin in data.anthropic_plugins.engineering_tools.plugins :
    plugin.name
    if plugin.organization_installation_preference_inherited
  ]
}
