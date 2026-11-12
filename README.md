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

| Attribute    | Environment variable   | Description                                                                                                  |
| ------------ | ---------------------- | ------------------------------------------------------------------------------------------------------------ |
| `api_key`    | `ANTHROPIC_API_KEY`    | Admin API key (`sk-ant-admin...`) or Claude Enterprise scoped key (`sk-ant-api01-...`). Optional, sensitive. |
| `auth_token` | `ANTHROPIC_AUTH_TOKEN` | OAuth bearer token with the `org:admin` scope. Optional, sensitive.                                          |
| `base_url`   | `ANTHROPIC_BASE_URL`   | API base URL. Defaults to `https://api.anthropic.com`.                                                       |

The provider is built on the official [Anthropic Go SDK](https://github.com/anthropics/anthropic-sdk-go) and accepts the same credentials as the SDK and the `ant` CLI. Set at most one of `api_key` and `auth_token`; a configured one is the only credential sent, regardless of the environment. When neither is set, the SDK resolves `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, the profile named by `ANTHROPIC_PROFILE`, Workload Identity Federation (`ANTHROPIC_FEDERATION_RULE_ID`, `ANTHROPIC_ORGANIZATION_ID`, `ANTHROPIC_IDENTITY_TOKEN_FILE`), then the active profile.

Prefer Workload Identity Federation in CI, so the pipeline never holds a long-lived admin secret. Service account and federation endpoints accept only an OAuth or federation token, never an Admin API key.

A Claude Enterprise (claude.ai) organization uses a scoped key created in claude.ai > Organization settings > API, set as `api_key` or `ANTHROPIC_API_KEY`. Only the types in this table work with such a key, and each needs the scopes listed. They also accept a Claude Console Admin API key unless the table says otherwise. Every other type works only with a Claude Console organization.

| Type                               | Claude Enterprise scopes                                                                    |
| ---------------------------------- | ------------------------------------------------------------------------------------------- |
| `anthropic_user`                   | `read:members`, `write:members`, plus `write:rbac_groups` to set the `managed` role         |
| `anthropic_invite`                 | `read:members`, `write:members`, plus `write:rbac_groups` for `managed` or `rbac_group_ids` |
| `anthropic_users`                  | `read:members`                                                                              |
| `anthropic_rbac_group`             | `read:rbac_groups`, `write:rbac_groups`                                                     |
| `anthropic_rbac_group_member`      | `read:rbac_groups`, `write:rbac_groups`                                                     |
| `anthropic_rbac_roles`             | `read:members`                                                                              |
| `anthropic_spend_limit`            | `read:spend_limits`, `write:spend_limits`. Claude Enterprise only                           |
| `anthropic_effective_spend_limits` | `read:spend_limits`. Claude Enterprise only                                                 |

## Resources

| Type                                  | API                                                            | SDK                                              |
| ------------------------------------- | -------------------------------------------------------------- | ------------------------------------------------ |
| `anthropic_workspace`                 | `/v1/organizations/workspaces`                                 | `client.Organization.Workspaces`                 |
| `anthropic_api_key`                   | `/v1/organizations/api_keys`                                   | `client.Organization.APIKeys`                    |
| `anthropic_service_account`           | `/v1/organizations/service_accounts`                           | `client.Organization.ServiceAccounts`            |
| `anthropic_workspace_service_account` | `/v1/organizations/workspaces/{workspace_id}/service_accounts` | `client.Organization.Workspaces.ServiceAccounts` |
| `anthropic_telemetry_destination`     | `/v1/organizations/telemetry_destinations`                     | `client.Beta.Organization.TelemetryDestinations` |
| `anthropic_workspace_rate_limit`      | `/v1/organizations/workspaces/{workspace_id}/rate_limits`      | `client.Organization.Workspaces.RateLimits`      |
| `anthropic_user`                      | `/v1/organizations/users`                                      | `client.Organization.Users`                      |
| `anthropic_invite`                    | `/v1/organizations/invites`                                    | `client.Organization.Invites`                    |
| `anthropic_rbac_group`                | `/v1/organizations/rbac_groups`                                | `client.Beta.Organization.RBACGroups`            |
| `anthropic_rbac_group_member`         | `/v1/organizations/rbac_groups/{rbac_group_id}/members`        | `client.Beta.Organization.RBACGroups.Members`    |
| `anthropic_spend_limit`               | `/v1/organizations/spend_limits`                               | `client.Beta.Organization.SpendLimits`           |

`anthropic_service_account` and `anthropic_workspace_service_account` accept only an OAuth access token with the `org:admin` scope, through `auth_token`, `ANTHROPIC_AUTH_TOKEN`, or Workload Identity Federation. An Admin API key is rejected. Destroying `anthropic_service_account` archives the service account, which cannot be undone and is rejected while a live federation rule still targets it. Destroying `anthropic_workspace_service_account` only removes the membership.

`anthropic_spend_limit` and `anthropic_effective_spend_limits` work only with a Claude Enterprise organization, not with a Claude Console organization. They need an API key created in claude.ai under Organization settings > API, with the `read:spend_limits` scope for reads and the `write:spend_limits` scope for changes. Spend limit increase requests are approved or denied in claude.ai or through the API, and are not managed as resources.

## Data sources

| Type                               | API                                        | SDK                                              |
| ---------------------------------- | ------------------------------------------ | ------------------------------------------------ |
| `anthropic_api_keys`               | `/v1/organizations/api_keys`               | `client.Organization.APIKeys`                    |
| `anthropic_telemetry_destinations` | `/v1/organizations/telemetry_destinations` | `client.Beta.Organization.TelemetryDestinations` |
| `anthropic_rate_limits`            | `/v1/organizations/rate_limits`            | `client.Organization.RateLimits`                 |
| `anthropic_users`                  | `/v1/organizations/users`                  | `client.Organization.Users`                      |
| `anthropic_rbac_roles`             | `/v1/organizations/rbac_roles`             | `client.Beta.Organization.RBACRoles`             |
| `anthropic_effective_spend_limits` | `/v1/organizations/spend_limits/effective` | `client.Beta.Organization.SpendLimits.Effective` |

## Example

```hcl
resource "anthropic_workspace" "production" {
  name = "Production"

  tags = {
    env  = "prod"
    team = "platform"
  }

  data_residency = {
    allowed_inference_geos = ["us"]
    default_inference_geo  = "us"
  }
}
```

The Admin API has no method to delete a workspace, so destroying `anthropic_workspace` archives it. Archiving cannot be undone and archives every API key created for the workspace. A workspace archived outside Terraform is removed from state and created again on the next apply.

`tags` is authoritative: removing a tag from the configuration removes it from the workspace. `external_key_id` is write-once, so Terraform rejects a plan that changes or removes it once set. When `data_residency` is unset, Terraform leaves the workspace's residency alone, so importing a workspace never plans a change to it. Configuring a `data_residency.workspace_geo` that differs from the workspace's replaces the workspace.

The Admin API cannot create an API key or return an existing key's secret, so `anthropic_api_key` only adopts a key created in the Claude Console: apply a plain resource block with no import and Terraform fails before it reaches the API. Once imported, Terraform can rename the key and move it between `active`, `inactive` and `archived`; `expired` stays read-only. Destroying the resource only stops managing the key, since an archived key cannot be brought back. Use `anthropic_api_keys` to list keys across the organization, optionally filtered by workspace, status or creator.

`anthropic_telemetry_destination` exports the spans of an organization's workspaces to an OpenTelemetry (OTLP/HTTP) collector, through the Admin API beta `telemetry-destinations-2026-08-11`. Header values such as the collector's `authorization` are write-only in the API, so Terraform keeps the configured values in state, marks them sensitive, and detects a header removed outside Terraform by its name. Destroying the resource deletes the destination. The API can disable a destination on its own after sustained delivery failures; `enabled` then reads `false`, `disabled_reason` says why, and the next apply enables it again. Use `anthropic_telemetry_destinations` to list destinations, optionally filtered by the workspaces they cover.

`anthropic_workspace_rate_limit` sets a workspace's rate-limit overrides for one rate-limit group, for example a `tokens_per_day` cap on a model group. Use `anthropic_rate_limits` to find the group's `rate_limit_id` from a model name. The resource manages only the limiter types in its `limits` map. Removing a type clears its override, and destroying the resource clears every override it manages, so the workspace inherits the organization's limit again. The API enforces the lower of the workspace and organization values, and a change takes effect within about an hour.

```hcl
data "anthropic_rate_limits" "haiku" {
  model = "claude-haiku-4-5"
}

resource "anthropic_workspace_rate_limit" "production_haiku" {
  workspace_id  = anthropic_workspace.production.id
  rate_limit_id = data.anthropic_rate_limits.haiku.rate_limits[0].id

  limits = {
    tokens_per_day = 5000000
  }
}
```

The Admin API cannot add a member directly, only invite one, so `anthropic_user` only adopts an existing member: import it, and Terraform manages the member's `role`. Apply a plain resource block with no import and Terraform fails before it reaches the API. Destroying the resource only stops managing the member. It never removes them from the organization, because a removal cannot be undone and the resource did not add them. Members who hold `owner`, `membership_admin` or `primary_owner` in a Claude Enterprise organization cannot be changed through the API. Use `anthropic_users` to look up members by email or role, for example to find the ID to import.

`anthropic_invite` sends an invitation email. An invite cannot be edited, so changing its email, role or groups withdraws it and sends a new one. An expired or withdrawn invite leaves state, so the next apply sends it again. An accepted invite stays in state. Destroying the resource withdraws a pending invite and leaves an accepted or expired one alone.

```hcl
resource "anthropic_invite" "new_hire" {
  email          = "newhire@example.com"
  role           = "managed"
  rbac_group_ids = ["rbac_group_01UvWxYzAbCdEfGhIjKlMn"]
}
```

`anthropic_rbac_group`, `anthropic_rbac_group_member` and `anthropic_rbac_roles` manage the RBAC groups of a Claude Enterprise organization. They need a key created in claude.ai > Organization settings > API for all linked organizations, set as `api_key`. The groups need the `read:rbac_groups` and `write:rbac_groups` scopes, and `anthropic_rbac_roles` needs `read:members`. Custom roles are attached to a group in claude.ai, so `role_ids` is read-only. A group provisioned by SCIM can be imported and read, but Terraform fails a plan that renames or deletes it, or adds or removes one of its members. Stop managing one with a `removed` block whose `lifecycle` sets `destroy = false`.

```hcl
data "anthropic_rbac_roles" "all" {}

resource "anthropic_rbac_group" "engineering" {
  name = "Engineering"
}

resource "anthropic_rbac_group_member" "alice" {
  rbac_group_id = anthropic_rbac_group.engineering.id
  user_id       = "user_01WCz1FkmYMm4gnmykNKUu3Q"
}
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the development setup, test workflow, and release process.
