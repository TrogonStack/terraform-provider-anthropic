resource "anthropic_telemetry_destination" "grafana" {
  display_name = "Grafana Cloud"
  endpoint_url = "https://otlp-gateway.example.com/otlp"

  headers = {
    authorization = var.otlp_authorization
  }

  traces = {
    content_mode = "metadata_only"
  }

  workspaces = {
    type          = "include"
    workspace_ids = [anthropic_workspace.production.id]
  }
}
