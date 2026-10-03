resource "anthropic_workspace" "production" {
  name          = "Production"
  display_color = "#6C5BB9"

  tags = {
    env  = "prod"
    team = "platform"
  }

  data_residency = {
    allowed_inference_geos = ["us"]
    default_inference_geo  = "us"
  }
}
