resource "anthropic_rbac_group" "engineering" {
  name = "Engineering"
}

resource "anthropic_rbac_group_member" "alice" {
  rbac_group_id = anthropic_rbac_group.engineering.id
  user_id       = "user_01WCz1FkmYMm4gnmykNKUu3Q"
}
