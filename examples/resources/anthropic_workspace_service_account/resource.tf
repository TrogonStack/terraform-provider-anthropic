resource "anthropic_workspace" "production" {
  name = "Production"
}

resource "anthropic_service_account" "github_actions_deploy" {
  name = "github-actions-deploy"
}

resource "anthropic_workspace_service_account" "github_actions_deploy" {
  workspace_id       = anthropic_workspace.production.id
  service_account_id = anthropic_service_account.github_actions_deploy.id
  workspace_role     = "workspace_developer"
}
