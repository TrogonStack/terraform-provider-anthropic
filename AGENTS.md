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

Runtime credentials the provider itself needs (not required to run the test suite, which never calls a real API) follow the Anthropic Go SDK credential chain: `ANTHROPIC_API_KEY` (an Admin API key), `ANTHROPIC_AUTH_TOKEN` (an `org:admin` OAuth token), `ANTHROPIC_PROFILE`, or Workload Identity Federation env vars. The `api_key` and `auth_token` attributes are mutually exclusive and replace the chain entirely (`option.WithoutEnvironmentDefaults()`), so no environment, profile, or federation credential is sent alongside them; `ANTHROPIC_BASE_URL` is still honored. Service account and federation endpoints accept only an OAuth or federation token.

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
- `data_residency` is an Optional + Computed `SingleNestedAttribute` with `objectplanmodifier.UseStateForUnknown()` and no default. Leaving it unset means Terraform does not manage residency: Create omits it so the API applies its defaults, Update never sends it, and an imported workspace in any geo plans no change. A static default would plan an update, or a replace through `workspace_geo`, for every workspace whose residency differs from it. `workspace_geo` and `default_inference_geo` are Optional + Computed with `UseStateForUnknown` and no default, and an unknown value is omitted from the request. `workspace_geo` uses `RequiresReplaceIfConfigured`, so only an explicitly configured geo that differs from state replaces the workspace (which archives it). `allowed_inference_geos` is Optional only: when `data_residency` is set, null maps to the API's `"unrestricted"` union variant. Geo values are deliberately not validated client-side, so a geo the API adds works without a provider release; an empty `allowed_inference_geos` set is rejected

### API key attributes

- The Admin API cannot create an API key or return an existing key's secret, so `anthropic_api_key` is import-only. `ModifyPlan` adds an error whenever `req.State.Raw.IsNull()` and the plan is not null, so a plain resource block with no prior state fails on `terraform plan`, before Create ever runs; Create keeps the same error as a backstop for any path that reaches it anyway
- `status` is Optional + Computed with `stringplanmodifier.UseStateForUnknown()` and `stringvalidator.OneOf("active", "inactive", "archived")`. Leaving it unset keeps whatever status the key already has; `expired` is a status the API assigns on its own and this resource cannot set, so it is not in the `OneOf` list, and an attempt to send it must surface as an API error rather than a client-side one
- `created_by`, `principal` and `scope` are Computed `SingleNestedAttribute`s shared between `anthropic_api_key` and `anthropic_api_keys` through one `apiKeyModel` struct and one `apiKeyFromAPI(key anthropic.APIKey) (apiKeyModel, diag.Diagnostics)` mapping function in `resource_api_key.go`. `created_by` and `principal` are null exactly when `CreatedBy.JSON.ID.Valid()` or `Principal.JSON.Type.Valid()` is false (the SDK's way of saying the field was null or omitted), never by checking the Go zero value. `scope.workspace_id` is null when `Scope.Type != "workspace"`
- Delete never calls the API. This resource only adopts keys it did not create, and archiving is unrecoverable, so removing the block from configuration only forgets the key in state; it never archives or deactivates it
- `anthropic_api_keys` paginates with `ListAutoPaging` and `Limit: 1000`, filters by `workspace_id`, `status` (including `expired`, since listing a key the resource cannot touch is still useful) and `created_by_user_id`, and derives `id` from whichever filters are set so two different filter sets never collide in the same state

### Testing

Tests use an in-memory fake of the Admin API, never real API calls:

- `fake_admin_api_test.go`: `fakeAdminAPI` is an `http.Handler` serving the workspace endpoints with a mutex-guarded map. It requires `X-Api-Key: testAPIKey`, answers unknown routes with 404, returns Anthropic-shaped errors (`{"type":"error","error":{...}}`), assigns `display_color` and `compartment_id`, and applies the API's `data_residency` defaults. `seed`, `archive` and `remove` simulate out-of-band changes; hold `mu` when calling them from a test
- `fake_api_keys_test.go`: adds the `/v1/organizations/api_keys` routes to the same `fakeAdminAPI`, with no create route, since the real Admin API has none either. `seedAPIKey` plants a key as if created in the Claude Console. The list handler caps every page at `fakeAPIKeyPageSize` regardless of the requested `limit`, so a test that seeds more keys than that exercises `ListAutoPaging`'s `after_id`/`has_more` cursor instead of always getting everything back in one page
- `setupTestServer()` serves the fake from an `httptest.Server`; `setupTestClient()` builds a client with `newClient` pointed at it and injects it as `testAPIClient`
- `live_test.go`: `TestLive_*` run against a real organization, skipped unless `TF_ACC` and `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN` are set; `mise run test` skips them. They create uniquely named workspaces, and destroy archives them. `TestLive_APIKeys` only reads the `anthropic_api_keys` data source with no filter, since the Admin API gives no way to create a key to tear down afterward

### Retry

The SDK retries on its own, honoring `Retry-After` and `x-should-retry`. `newClient` raises the limit with `option.WithMaxRetries`; there is no configuration attribute and no custom retry code.

## CI

- PR: lint + test + build (GitHub Actions)
- Release: release-please + goreleaser on push to main
