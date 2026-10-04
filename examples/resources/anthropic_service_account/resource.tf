resource "anthropic_service_account" "github_actions_deploy" {
  name              = "github-actions-deploy"
  description       = "Deploys from GitHub Actions through workload identity federation"
  organization_role = "developer"
}
