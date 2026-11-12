resource "anthropic_plugin_marketplace" "engineering_tools" {
  provider                        = anthropic.plugins
  default_installation_preference = "available"
}

import {
  provider = anthropic.plugins
  to       = anthropic_plugin_marketplace.engineering_tools
  id       = "marketplace_01VbNcMxZaSdFgHjKlQwErTy"
}
