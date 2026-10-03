# terraform-provider-anthropic

**A Terraform provider that manages an Anthropic organization as code.** It covers the [Admin API](https://platform.claude.com/docs/en/api/administration-api) behind a single provider configuration.

**It exists because organization structure otherwise drifts between the Claude Console and whoever clicked last.** Workspaces get created by hand, members gain and lose access ad hoc, and neither leaves the kind of record that a code review or a rollout pipeline can rely on. Expressing the organization as Terraform configuration puts those changes under review and lets Anthropic live alongside the rest of your infrastructure.

This provider is not affiliated with or endorsed by Anthropic.

## Provider configuration

```hcl
provider "anthropic" {
  auth_token = var.anthropic_auth_token
}
```

| Attribute       | Environment variable      | Description                                                                 |
| --------------- | ------------------------- | --------------------------------------------------------------------------- |
| `auth_token`    | `ANTHROPIC_AUTH_TOKEN`    | OAuth bearer token with the `org:admin` scope, or a Workload Identity Federation token. Optional, sensitive. |
| `admin_api_key` | `ANTHROPIC_ADMIN_API_KEY` | Admin API key (`sk-ant-admin...`). Optional, sensitive.                     |
| `base_url`      | `ANTHROPIC_BASE_URL`      | API base URL. Defaults to `https://api.anthropic.com`.                      |

Each attribute falls back to its environment variable when unset in configuration. Set exactly one of `auth_token` and `admin_api_key`. Prefer `auth_token` minted through Workload Identity Federation in CI, so the pipeline never holds a long-lived admin secret.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup, test workflow, and release process.
