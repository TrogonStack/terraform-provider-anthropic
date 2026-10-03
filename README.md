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

| Attribute    | Environment variable   | Description                                                        |
| ------------ | ---------------------- | ------------------------------------------------------------------ |
| `api_key`    | `ANTHROPIC_API_KEY`    | Admin API key (`sk-ant-admin...`). Optional, sensitive.            |
| `auth_token` | `ANTHROPIC_AUTH_TOKEN` | OAuth bearer token with the `org:admin` scope. Optional, sensitive. |
| `base_url`   | `ANTHROPIC_BASE_URL`   | API base URL. Defaults to `https://api.anthropic.com`.             |

The provider is built on the official [Anthropic Go SDK](https://github.com/anthropics/anthropic-sdk-go) and accepts the same credentials as the SDK and the `ant` CLI. Set at most one of `api_key` and `auth_token`. When neither is set, the SDK resolves `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, the profile named by `ANTHROPIC_PROFILE`, Workload Identity Federation (`ANTHROPIC_FEDERATION_RULE_ID`, `ANTHROPIC_ORGANIZATION_ID`, `ANTHROPIC_IDENTITY_TOKEN_FILE`), then the active profile.

Prefer Workload Identity Federation in CI, so the pipeline never holds a long-lived admin secret. Service account and federation endpoints accept only an OAuth or federation token, never an Admin API key.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup, test workflow, and release process.
