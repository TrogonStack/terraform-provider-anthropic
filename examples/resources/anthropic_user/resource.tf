data "anthropic_users" "jane" {
  email = "jane@example.com"
}

resource "anthropic_user" "jane" {
  role = "managed"
}

import {
  to = anthropic_user.jane
  id = data.anthropic_users.jane.users[0].id
}
