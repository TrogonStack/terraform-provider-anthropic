# terraform-provider-anthropic

Always use `/claude-md-improver` when updating this file.

Terraform provider for managing an Anthropic organization through the Admin API.

- **Module**: `github.com/TrogonStack/terraform-provider-anthropic`
- **Package**: `internal/provider/` (single flat package, all resources here)

## Commands

```bash
mise run test          # go test -count=1 -cover ./...
mise run lint          # golangci-lint run --fix ./...
mise run build         # full CI pipeline (download, tidy, lint, test, docs, diff)
mise run docs          # regenerate docs/ from schema descriptions
mise run test:live     # live acceptance tests, needs a credential and a sandbox organization
```

Runtime credentials the provider itself needs (not required to run the test suite, which never calls a real API) follow the Anthropic Go SDK credential chain: `ANTHROPIC_API_KEY` (an Admin API key), `ANTHROPIC_AUTH_TOKEN` (an `org:admin` OAuth token), `ANTHROPIC_PROFILE`, or Workload Identity Federation env vars. The `api_key` and `auth_token` attributes override the chain and are mutually exclusive. Service account and federation endpoints accept only an OAuth or federation token.

## Skills

Always load `terraform-provider-dev` when working on a resource or data source.

## Architecture

- File naming: `resource_<name>.go`, `resource_<name>_test.go`, `data_source_<name>.go`
- Provider client: `*anthropic.Client` from [`anthropic-sdk-go`](https://github.com/anthropics/anthropic-sdk-go), built by `newClient` in `client.go`. Resources cast `req.ProviderData.(*anthropic.Client)` and call `client.Organization.*` (or `client.Beta.Organization.*` for beta-only endpoints) with the CRUD method's `ctx`
- New resources must be registered in `provider.go` `Resources()` / `DataSources()`

## Conventions

### Resource naming

Use the same terminology as the Admin API and the SDK (`/v1/organizations/workspaces` and `client.Organization.Workspaces` are `anthropic_workspace`). Never abbreviate to jargon that doesn't appear in the API surface.

### Retry

The SDK retries on its own, honoring `Retry-After` and `x-should-retry`. `newClient` raises the limit with `option.WithMaxRetries`; there is no configuration attribute and no custom retry code.

## CI

- PR: lint + test + build (GitHub Actions)
- Release: release-please + goreleaser on push to main
