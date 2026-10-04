resource "anthropic_api_key" "ci" {
  name   = "CI"
  status = "active"
}

import {
  to = anthropic_api_key.ci
  id = "apikey_01Rj2N8SVvo6BePZj99NhmiT"
}
