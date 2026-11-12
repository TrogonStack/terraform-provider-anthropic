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

### Service account attributes

- `name` is Required and `RequiresReplace`: the API has no rename, so a name change archives the old service account and creates a new one
- `description` is Optional; the API stores an unset description as `""`, which `optionalString()` maps to null. Update sends `param.Null[string]()` to clear it and `anthropic.String(...)` to set it, matching the workspace `tags` clear-with-explicit-value pattern but at the field level instead of a whole map
- `organization_role` is Optional + Computed with `UseStateForUnknown` and no static default, because the API defaults to `developer` on create when omitted. Update sends it only when configured and changed
- `anthropic_workspace_service_account`'s `id` is `"<workspace_id>/<service_account_id>"`; `ImportState` splits on `/` and sets both attributes directly rather than passthrough
- `fake_service_accounts_test.go` holds the service account and membership fake handlers and state, registered onto the shared `fakeAdminAPI` mux through `registerServiceAccountRoutes()`, called once from `newFakeAdminAPI()`
- `anthropic_workspace_service_account` manages only an explicit membership; a `Get` on the default workspace with no explicit row returns an implicit `workspace_user` membership, and `Read` treats that as gone so Terraform plans a new `Add` rather than an `Update`, which the API rejects on an implicit membership
- `Read` and `Delete` both treat a non-404 error from the membership call as "gone" once a secondary `Workspaces.Get` call shows the workspace archived or missing, since the Admin API returns 400, not 404, for an archived workspace

### Telemetry destination attributes

- Every call goes through `client.Beta.Organization.TelemetryDestinations`, which sends `anthropic-beta: telemetry-destinations-2026-08-11` itself; the fake rejects a request without it
- `headers` is Optional + Sensitive and write-only: the API returns only `header_names`. State keeps the configured values, `Read` drops a configured header whose name is missing from `header_names` so it shows as drift, and `ImportStateVerify` ignores `headers`. Update sends every configured header again whenever headers change, since the API charges each stored value it is not sent as 4,096 bytes of its 16,384-byte limit, and sends `null` for a stored name that left the configuration through `option.WithJSONSet("headers", ...)`, because the SDK's `map[string]string` cannot carry a null. Setting a header requires `endpoint_url` in the same request, so Update always sends it with headers. A hostname or port change wipes every stored header, so headers are resent in that request and never nulled. `headers_wo` and `headers_wo_version` are the write-only alternative: `headers_wo` is read from `req.Config` and never reaches plan or state, `headers` and `headers_wo` conflict, and the two `_wo` attributes require each other. Update resends `headers_wo` (and nulls stored names absent from it) only when `headers_wo_version` or the endpoint host changes; any other update leaves headers alone. `ModifyPlan` only warns when configured names differ from `header_names`, because Read cannot see config
- `enabled` is Optional + Computed with a `true` default and maps to the `state` union. The API can disable a destination itself (`sustained_failure`, `invalid_address`, `redirect`), so `Read` reports `false` and `disabled_reason`, and a configuration with `enabled = true` plans an update that enables it again. Create cannot send `disabled`, so a destination configured with `enabled = false` gets a follow-up Update
- `traces` is Optional + Computed. Update sends only changed fields; `endpoint_url` and `max_content_bytes` are nullable, so removing them from a configured block sends `param.Null`
- `workspaces.workspace_ids` is not returned by Get. `fetch` lists it through the destination's workspaces endpoint for `include` and `exclude` types, and null means an empty list. A request body carries at most 100 IDs, a list holds at most 1,000, and the API refuses to replace a list above 750. Create and a type change send the first 100 IDs in the body and Add the rest one by one; an unchanged type diffs the sets and uses per-workspace Add and Remove, treating a 404 on Remove as done
- Update turns a 409 into a diagnostic that asks for another apply, and Delete treats a 404 as already gone
- `fake_telemetry_destinations_test.go` registers its routes through `registerTelemetryDestinationRoutes()` and models header merge with null removal, the host-change wipe, the 100-ID body cap, the 10-destination limit, newest-first pages of `fakeTelemetryPageSize`, and records update bodies in `telemetryUpdateBodies`. `telemetryConflicts` makes every update return 409 until cleared, because the SDK retries a 409

### Workspace rate limit attributes

- `anthropic_workspace_rate_limit` manages a workspace's overrides for one rate-limit group. Its `id` is `"<workspace_id>/<rate_limit_id>"`, and both IDs are `RequiresReplace`. `rate_limit_id` is the top-level `id` of an `anthropic_rate_limits` entry (`rlt_...`), not `group.id`. The API returns the group type as `group.type`; the `group_type` field on entries is gone in API version `2026-10-21`
- `limits` is a Required map of limiter type to value, from 0 to 9007199254740991. Only the types in `limits` are managed: Create and Update POST them, Update and Delete send `param.Null[int64]()` (it marshals as `"value":null`) for a type that left the map, and Read drops every other override the list shows. Import adopts every override of the group and fails when there is none
- Limiter types are not a closed `OneOf`, because the API adds types (`fast_itpmca`, `fast_otpm`). `ValidateConfig` warns about a type outside `settableLimiterTypes`, and `ModifyPlan` looks up the group type through the org list and fails a known type that `limiterTypesByGroupType` says the group rejects. A failed lookup leaves the check to the API, which validates the whole request before it changes anything
- The workspace list can lag a write, while the POST response is current. Create and Update check the response against the plan, keep the plan in state, and store a `pending_write` private state key with the written values and the values a lagging list can still show (the earlier pending write, the list before the write, and the prior state). For `rateLimitListLag` (one hour), Read keeps a written value while the list shows one of those stale values, and reports any other value as drift. Read clears the key once the list shows every written value. `currentTime` is a package variable so tests can move the clock
- A 404 on the list removes the resource. Any other list or POST error removes it, or is ignored on Delete, once `Workspaces.Get` shows the workspace archived or missing
- `fake_rate_limits_test.go` registers its routes through `registerRateLimitRoutes()`. It serves the API reference's example entries plus placeholder Haiku and web search groups, applies the group rules, records POST bodies in `rateLimitUpdateBodies`, and pages with `fakePage`. `rateLimitListLags` freezes `listedWorkspaceRateLimits` while `workspaceRateLimits` changes, `catchUpRateLimitList` ends the lag, and `setRateLimitOverride` simulates an out-of-band change. The list answers 404 for an archived workspace, which the API reference does not specify

### User and invite attributes

- Both use `client.Organization.Users` and `client.Organization.Invites`. The `client.Beta.Organization` variants are identical except for a `?beta=true` query, so they add nothing. A Claude Enterprise scoped key (`sk-ant-api01-...`, `x-api-key`) works only with these types; a Console Admin API key works too
- `anthropic_user` is import-only like `anthropic_api_key`, through the same `ModifyPlan` check. `role` is Required and not validated client-side, because Console and Enterprise accept different roles and the API rejects the rest. Delete never calls the API, since removing a member is unrecoverable and the resource did not add them. A 404 on Read removes the resource, so the next plan fails with the import-only error
- `userModel` and `userFromAPI` in `resource_user.go` are shared with `anthropic_users`, which filters by `email` and `roles`, sorts `roles` for a stable `id`, and pages with `ListAutoPaging` and `Limit: 1000`
- `anthropic_invite` makes every input `RequiresReplace`, except that `email` uses `RequiresReplaceIf` to skip a case-only change, which Update writes to state without an API call. `ModifyPlan` fails a replacement of an `accepted` invite, because Delete keeps it and the API rejects a new invite for a member. `ModifyPlanResponse.RequiresReplace` starts empty in the resource `ModifyPlan`, so it compares plan and state itself. `rbac_group_ids` is Optional + Computed with an empty-set default, because the API returns `[]` for none; Create omits an empty set. Read removes the resource on a 404 or a `deleted` or `expired` status, so the next apply sends a new invite; an `accepted` invite stays. Read keeps a configured email that differs from the API's only in case. Delete reads first and withdraws only a `pending` invite, because the API answers 400 for an accepted or expired one, and ignores a 404
- `fake_users_test.go` registers its routes through `registerUserRoutes()` and keeps its state in `fakeAdminAPI.users`. It models a Claude Enterprise organization: only `user` and `managed` are assignable, administrative members are fixed, member pages hold `fakeUserPageSize` items newest first, a duplicate pending invite or an existing member's email is rejected, and a withdrawn invite reads as `deleted`. `inviteRequests` records create bodies and `removedUsers` records member removals

### RBAC group attributes

- `anthropic_rbac_group`, `anthropic_rbac_group_member` and `anthropic_rbac_roles` serve Claude Enterprise organizations only. They call `client.Beta.Organization.RBACGroups` and `client.Beta.Organization.RBACRoles`, whose paths carry `?beta=true`; the fake rejects a request without it. The key is created in claude.ai with the `read:rbac_groups`, `write:rbac_groups` and `read:members` scopes and goes in `api_key`
- `name` is Required and renamed in place. `role_ids` is a Computed set with no `UseStateForUnknown`, because roles are attached in claude.ai, not through the API. The API returns `role_ids` as null when role data is temporarily unavailable, so `applyRBACGroup` keeps the prior value
- A group with `source_type` `scim` is read-only through the API. `ModifyPlan` on the group fails a rename or destroy of one from state, and `ModifyPlan` on the member reads the group and fails an add or remove. Update, Create and Delete re-read the group after an error and report `rbacGroupSCIMSummary` instead of the raw 400. The error points to a `removed` block with `destroy = false`
- `anthropic_rbac_group_member`'s `id` is `"<rbac_group_id>/<user_id>"`, and both IDs are `RequiresReplace`. The API has no single-member Get, so `findMember` pages through the member list. A missing member, a user who left the enterprise, or a 404 on the group removes the resource
- `anthropic_rbac_roles` lists every role and then each role's permissions, one request per role. A permission's resource identifiers are null unless the API sends them
- `fake_rbac_groups_test.go` registers its routes through `registerRBACGroupRoutes()`. It rejects a write to a SCIM group with 400, answers 404 for a user outside `users` and 400 for a duplicate member, lists only members still in `users`, and pages with `fakePage`. `roleDataUnavailable` makes every group return `role_ids` as null. `seedRBACUser`, `seedRBACGroup`, `seedRBACGroupMember`, `seedRBACRole`, `removeRBACGroup` and `deleteRBACGroupMember` simulate out-of-band changes

### Spend limit attributes

- The Spend Limits API (`client.Beta.Organization.SpendLimits`) serves Claude Enterprise organizations only. The key comes from claude.ai > Organization settings > API (`sk-ant-api01-...`) and needs `read:spend_limits` for GET and `write:spend_limits` for POST and DELETE
- `anthropic_spend_limit` manages one per-user override. Enterprise `Set` accepts only the `user` scope, so `user_id` is Required. The Console `organization` and `workspace` scopes are an early access preview and are not modeled. Seat tier, group and organization defaults are configured in claude.ai only
- `Set` is an upsert keyed on (scope, `period`), so Create adopts an existing override and Update overwrites it in place. `user_id` and `period` are `RequiresReplace`, and Update fails when the response carries another `id`. The `id` is the `spl_...` from the response, and import passes it through. Read rejects a limit whose scope is not `user`
- `amount` is the API's integer decimal string in the minor unit of `currency`, validated as `^(0|[1-9][0-9]*)$`. A null `amount` is sent as `"amount":null`, which is an explicit no-limit override and not the absence of one. Read uses `JSON.Amount.Valid()`. `period` is Optional and Computed with no `OneOf`, because the docs call it an open set and support only `monthly` today. `updated_at` has no `UseStateForUnknown`, so an amount change plans it as unknown
- Delete removes the override, so the member falls back to an inherited limit. A 404 on Read removes the resource, and Delete ignores it
- `anthropic_effective_spend_limits` lists `Effective.ListAutoPaging` with the `user_ids` (at most 100) and `periods` (at most 3) filters. The API pages by member, so a page can hold more rows than `limit`. `actor`, `scope` and `source` flatten their unions into one object, and an attribute of another variant is null. `source` is an open set
- Increase requests (`SpendLimits.IncreaseRequests`) are approved or denied once and are not desired state, so they are not modeled
- `fake_spend_limits_test.go` registers its routes through `registerSpendLimitRoutes()` and keeps its state in `f.spendLimits`. It seeds four members, a `premium` seat tier default and monthly and weekly organization defaults, resolves the effective limit from the override, then the seat tier, then the organization, and pages by member. It accepts only the `user` scope and the `monthly` period, records POST bodies, and `deleteRaces` makes DELETE answer 404

### Plugin attributes

- `anthropic_plugin_installation_setting`, `anthropic_plugin_marketplace` and `anthropic_plugins` call `client.Beta.Organization.Plugins` and `PluginMarketplaces`, which send `anthropic-beta: ce-plugins-2026-09-01` themselves. The real API answers 404 without that header, and so does the fake. The API serves only Claude Enterprise organizations and takes an `sk-ant-api01-` key from claude.ai with `read:plugins` and `write:plugins` (writes do not grant reads), not an Admin API key
- `installation_preference` and `default_installation_preference` are not a closed `OneOf`: the API returns a value it does not yet name as stored
- The installation setting `id` is `"<plugin_id>/<target>"`, where `target` is `organization` or an `rbac_group_` ID, and both are `RequiresReplace`. The API has no per-target get, so `find` lists the plugin's settings with the `target_type` filter. Set upserts, so Create refuses a target that already holds a setting and asks for an import. Delete calls `Remove` and ignores a 404
- `anthropic_plugin_marketplace` is import-only on the `anthropic_api_key` pattern and manages only `default_installation_preference` (Optional + Computed, `UseStateForUnknown`). A default cannot be removed once set, so Delete never calls the API. A marketplace with no default reads `not_available`
- No resource uploads plugin versions or changes the served version, because pinning cannot be undone and a destroy could not restore the earlier state
- `fake_plugins_test.go` registers its routes through `registerPluginRoutes()` and keeps its state in `fakeAdminAPI.plugins`. `seedPluginMarketplace`, `seedPlugin`, `seedPluginTargetRBACGroup` and `setPluginSetting` plant data; an `OwnerUserID` makes a plugin or marketplace member-owned, so the setting endpoints return 404 and the marketplace update returns 403. Lists page with `fakePage`

### Workspace member attributes

- `anthropic_workspace_member`'s `id` is `"<workspace_id>/<user_id>"`; both are `RequiresReplace`, `workspace_role` updates in place, and `ImportState` splits on `/`
- `workspace_role` is a closed `OneOf` of the four `NoBillingWorkspaceRole` values. `Add` cannot send `workspace_billing`, and the workspaces guide says it is never manually assigned, although the SDK's `Update` type accepts it
- `WorkspaceMember` has no `implicit` flag, unlike `ServiceAccountWorkspaceMember`, and the docs say organization users and developers are added to each workspace explicitly, so the default workspace gets no special case. Organization admins and billing members inherit `workspace_admin` and `workspace_billing` in every workspace and cannot be removed, so Create refuses any membership `Get` already returns rather than adopting it
- `Read` and `Delete` use the same secondary `Workspaces.Get` as `anthropic_workspace_service_account` for an archived workspace's 400
- `Read` removes an inherited membership, the way `anthropic_workspace_service_account` drops an implicit one. `workspace_billing` is always inherited. `workspace_admin` is inherited when `Users.Get` returns the organization role `admin`, so only that role costs a second request
- `fake_workspace_members_test.go` registers its routes through `registerWorkspaceMemberRoutes()` and keeps user memberships in `workspaceUsers` and their organization roles in `f.users`, apart from the service account `workspaceMembers` map. `seedOrganizationUser(role)` creates a user; an `admin` or `billing` user gets the inherited role from `Get` and `List`, and `Update` and `Remove` reject it. `TestLive_WorkspaceMember` runs only when `ANTHROPIC_LIVE_USER_ID` names an organization user or developer

### Testing

Tests use an in-memory fake of the Admin API, never real API calls:

- `fake_admin_api_test.go`: `fakeAdminAPI` is an `http.Handler` serving the workspace endpoints with a mutex-guarded map. It requires `X-Api-Key: testAPIKey`, answers unknown routes with 404, returns Anthropic-shaped errors (`{"type":"error","error":{...}}`), assigns `display_color` and `compartment_id`, and applies the API's `data_residency` defaults. `seed`, `archive` and `remove` simulate out-of-band changes; hold `mu` when calling them from a test
- `fake_api_keys_test.go`: adds the `/v1/organizations/api_keys` routes to the same `fakeAdminAPI`, with no create route, since the real Admin API has none either. `seedAPIKey` plants a key as if created in the Claude Console. The list handler caps every page at `fakeAPIKeyPageSize` regardless of the requested `limit`, so a test that seeds more keys than that exercises `ListAutoPaging`'s `after_id`/`has_more` cursor instead of always getting everything back in one page
- `setupTestServer()` serves the fake from an `httptest.Server`; `setupTestClient()` builds a client with `newClient` pointed at it and injects it as `testAPIClient`
- `live_test.go`: `TestLive_*` run against a real organization, skipped unless `TF_ACC` and `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN` are set; `mise run test` skips them. They create uniquely named workspaces, and destroy archives them. `TestLive_APIKeys` only reads the `anthropic_api_keys` data source with no filter, since the Admin API gives no way to create a key to tear down afterward. `TestLive_RateLimits` only reads `anthropic_rate_limits`; no live test sets a rate limit, because an override changes what the organization can use

### Retry

The SDK retries on its own, honoring `Retry-After` and `x-should-retry`. `newClient` raises the limit with `option.WithMaxRetries`; there is no configuration attribute and no custom retry code.

## CI

- PR: lint + test + build (GitHub Actions)
- Release: release-please + goreleaser on push to main
