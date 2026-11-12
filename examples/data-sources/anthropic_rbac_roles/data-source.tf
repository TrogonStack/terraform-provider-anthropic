data "anthropic_rbac_roles" "all" {}

output "rbac_role_names" {
  value = { for role in data.anthropic_rbac_roles.all.rbac_roles : role.id => role.display_name }
}
