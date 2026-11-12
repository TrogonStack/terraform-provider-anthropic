resource "anthropic_invite" "new_hire" {
  email          = "newhire@example.com"
  role           = "managed"
  rbac_group_ids = ["rbac_group_01UvWxYzAbCdEfGhIjKlMn"]
}
