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

Runtime credentials the provider itself needs (not required to run the test suite, which never calls a real API):

- `ANTHROPIC_AUTH_TOKEN`: an `org:admin` OAuth token or a Workload Identity Federation token, sent as `Authorization: Bearer`
- `ANTHROPIC_ADMIN_API_KEY`: an Admin API key (`sk-ant-admin...`), sent as `x-api-key`

Exactly one must be set. Service account, federation issuer and federation rule endpoints accept only the OAuth token.

## Skills

Always load `terraform-provider-dev` when working on a resource or data source.

## Architecture

- File naming: `resource_<name>.go`, `resource_<name>_test.go`, `data_source_<name>.go`
- Provider client: `*apiClient` in `client.go` holds the retrying HTTP client from `retry.go`, the base URL and a `credential`. `newRequest` sets `anthropic-version` and the auth header
- Auth: `resolveCredential` turns the configured values into an `adminAPIKey` or `authToken`, both implementing `credential`
- New resources must be registered in `provider.go` `Resources()` / `DataSources()`

## Conventions

### Resource naming

Use the same terminology as the Admin API (`/v1/organizations/workspaces` is `anthropic_workspace`). Never abbreviate to jargon that doesn't appear in the API surface.

### Retry

Automatic retry on 429 and 5xx except 501. No configuration attribute: the transport in `retry.go` is fixed, and `go-retryablehttp`'s default backoff honors `Retry-After` on 429.

## CI

- PR: lint + test + build (GitHub Actions)
- Release: release-please + goreleaser on push to main
