data "anthropic_workspace_members" "production" {
  workspace_id = anthropic_workspace.production.id
}

output "production_admins" {
  value = [
    for member in data.anthropic_workspace_members.production.members :
    member.user_id
    if member.workspace_role == "workspace_admin"
  ]
}
