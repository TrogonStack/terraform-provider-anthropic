resource "anthropic_workspace" "production" {
  name = "Production"
}

resource "anthropic_workspace_member" "alice" {
  workspace_id   = anthropic_workspace.production.id
  user_id        = "user_01WxYzAbCdEfGhJkMnPqRsTu"
  workspace_role = "workspace_developer"
}
