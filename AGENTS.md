# terraform-provider-anthropic

Always use `/claude-md-improver` when updating this file.

Terraform provider for managing an Anthropic organization through the Admin API.

- **Module**: `github.com/TrogonStack/terraform-provider-anthropic`
- **Package**: `internal/provider/` (single flat package, all resources here)

## Commands

```bash
mise run test          # go test -count=1 -cover -skip TestLive ./...
mise run lint          # golangci-lint run --fix ./...
mise run build         # full CI pipeline (download, tidy, lint, test, docs, diff)
mise run docs          # regenerate docs/ from schema descriptions
mise run test:live     # live acceptance tests, needs a credential and a sandbox organization
```

Single test:

```bash
TF_ACC=1 go test ./internal/provider/ -v -run TestAccWorkspace
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

### Not-found handling

`isNotFound(err)` in `errors.go` unwraps with `errors.As` into `*anthropic.Error` and matches `StatusCode == 404`. Never compare error strings.

- **Read**: a 404, or a workspace with a non-zero `ArchivedAt`, calls `resp.State.RemoveResource(ctx)` so the next apply creates it again
- **Delete**: return without error when the resource is already gone

### Destroy semantics

The Admin API has no workspace delete. `anthropic_workspace` Delete reads the workspace first, skips one that is missing or already archived, then calls `Archive` and ignores a 404. Archiving cannot be undone and archives every API key in the workspace; say so in the schema description and README of any resource whose destroy is not a real delete.

### Workspace attributes

- `tags` is Optional + Computed with an empty-map default, so Terraform owns the whole map. Update always sends the full map; a non-nil empty map marshals as `"tags":{}` despite `omitzero`, which is what clears tags. The API docs do not say whether update replaces or merges, so Update fails with a diagnostic if the response tags differ from the plan
- `external_key_id` is write-once in the API. `writeOnceString()` in `helpers.go` rejects a plan that changes or removes a set value; it is never `RequiresReplace`, because replacing a workspace archives it
- `data_residency` is an Optional + Computed `SingleNestedAttribute` whose object default matches the API defaults (`us`, unrestricted, `global`), so leaving it unset never drifts. `allowed_inference_geos` is Optional only: null maps to the API's `"unrestricted"` union variant. `workspace_geo` is `RequiresReplace`. Geo values are not validated client-side; the API validates them

### Testing

Tests use an in-memory fake of the Admin API, never real API calls:

- `fake_admin_api_test.go`: `fakeAdminAPI` is an `http.Handler` serving the workspace endpoints with a mutex-guarded map. It requires `X-Api-Key: testAPIKey`, answers unknown routes with 404, returns Anthropic-shaped errors (`{"type":"error","error":{...}}`), assigns `display_color` and `compartment_id`, and applies the API's `data_residency` defaults. `archive` and `remove` simulate out-of-band changes; hold `mu` when calling them from a test
- `setupTestServer()` serves the fake from an `httptest.Server`; `setupTestClient()` builds a client with `newClient` pointed at it and injects it as `testAPIClient`
- `live_test.go`: `TestLive_*` run against a real organization, skipped unless `TF_ACC` and `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN` are set; `mise run test` skips them. They create uniquely named workspaces, and destroy archives them

### Retry

The SDK retries on its own, honoring `Retry-After` and `x-should-retry`. `newClient` raises the limit with `option.WithMaxRetries`; there is no configuration attribute and no custom retry code.

## CI

- PR: lint + test + build (GitHub Actions)
- Release: release-please + goreleaser on push to main
