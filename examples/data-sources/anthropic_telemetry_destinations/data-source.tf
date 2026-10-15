data "anthropic_telemetry_destinations" "production" {
  workspace_ids = [anthropic_workspace.production.id]
}

output "disabled_telemetry_destinations" {
  value = [
    for destination in data.anthropic_telemetry_destinations.production.telemetry_destinations :
    destination.display_name
    if !destination.enabled
  ]
}
