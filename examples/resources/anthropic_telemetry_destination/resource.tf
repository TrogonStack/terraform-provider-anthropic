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

ephemeral "onepassword_item" "otlp" {
  vault = "Infrastructure"
  title = "OTLP collector"
}

resource "anthropic_telemetry_destination" "grafana_write_only" {
  display_name = "Grafana Cloud"
  endpoint_url = "https://otlp-gateway.example.com/otlp"

  headers_wo = {
    authorization = ephemeral.onepassword_item.otlp.password
  }
  headers_wo_version = 1

  workspaces = {
    type = "all"
  }
}
