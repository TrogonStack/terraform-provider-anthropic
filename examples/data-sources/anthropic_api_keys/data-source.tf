data "anthropic_api_keys" "production" {
  workspace_id = anthropic_workspace.production.id
}

output "never_expiring_api_keys" {
  value = [
    for key in data.anthropic_api_keys.production.api_keys :
    key.name
    if key.expires_at == null
  ]
}
