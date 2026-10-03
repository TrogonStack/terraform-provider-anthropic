---
name: terraform-provider-dev
description: >
  Use this skill when developing terraform-provider-anthropic: adding
  resources or data sources, designing schemas, implementing CRUD operations,
  plan modification, state upgrades, import, validation, acceptance testing,
  debugging, or any Terraform Plugin Framework work in Go. Also use when the
  user asks about terraform provider patterns, attribute types, or how to
  structure tests. This is the primary development skill for this repository.
---

# Terraform Provider Development (Plugin Framework)

## Mental Model

- Provider = Go server implementing Terraform RPCs (GetProviderSchema, PlanResourceChange, ApplyResourceChange, ReadResource, etc.)
- Resource = struct implementing `resource.Resource` interface: Metadata, Schema, Configure, Create, Read, Update, Delete
- DataSource = struct implementing `datasource.DataSource` interface: Metadata, Schema, Configure, Read
- Schema defines the "shape" of config/plan/state: attributes (leaf values) and blocks (nested structures)
- Plan then Apply: Terraform calls PlanResourceChange (propose changes), then ApplyResourceChange (execute)
- State = Terraform's record of the real world; Plan = expected post-apply state
- Computed attributes: set by the provider from API responses (IDs, timestamps, server-generated values)
- Plugin Framework uses strong Go types: `types.String`, `types.Bool`, `types.Int64`, `types.List`, etc.
- Null vs Unknown: null means the user did not set it; unknown means the value will be known after apply (planned computed)

---

## This Provider: Conventions

`anthropic_workspace` (`resource_workspace.go`) is the first and reference resource. The provider surface will grow to cover the rest of the Admin API (workspace members, users, invites, API keys, service accounts, federation issuers and rules); copy the workspace resource's shape unless the API forces something else. In `internal/provider/`:

- **Package**: `internal/provider` (single flat package; every resource and data source lives here)
- **File naming**: `resource_<name>.go`, `resource_<name>_test.go`, `data_source_<name>.go`
- **Provider**: `anthropicProvider`. `Metadata` sets `resp.TypeName = "anthropic"`, so every resource type name is `anthropic_<name>`
- **Provider schema**: two Optional, Sensitive attributes, `api_key` (an Admin API key, `sk-ant-admin...`) and `auth_token` (an OAuth or workload-identity-federation token with the `org:admin` scope). Setting both is `errConflictingCredential`. If neither is set, the official Go SDK's own credential chain applies, in order: `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_PROFILE`, workload identity federation env vars (`ANTHROPIC_FEDERATION_RULE_ID`, `ANTHROPIC_ORGANIZATION_ID`, `ANTHROPIC_IDENTITY_TOKEN_FILE`), then the active or default profile. There is no provider-level "no credential" error; an unresolved credential surfaces as a failure on the first API call. `base_url` is Optional and falls back to the SDK's own default
- **Provider client**: the official Go SDK, `github.com/anthropics/anthropic-sdk-go` v1.78.0. `client.go`'s `newClient(clientConfig{apiKey, authToken, baseURL string}) (*anthropic.Client, error)` always passes `option.WithMaxRetries(5)` and `option.WithBaseURL` when `base_url` is set. A configured credential adds `option.WithAPIKey`/`option.WithAuthToken` together with `option.WithoutEnvironmentDefaults()`, because `anthropic.NewClient` otherwise prepends `DefaultClientOptions()` and an environment credential of the other kind would be sent too; that path re-adds the SDK's default HTTP client timeout and the `ANTHROPIC_BASE_URL` fallback. No custom retry logic
- **Client injection**: each resource's `Configure` casts `req.ProviderData.(*anthropic.Client)` and calls `client.Organization.*` with the CRUD method's `ctx`
- **Registration**: `Resources()` in `provider.go` lists `newWorkspace`. `DataSources()` is still empty
- **Helpers** (`helpers.go`): `rsId()` (Computed `id` with `UseStateForUnknown`), `isConfigured`, `optionalString` (API `""`/`null` to a null string), `timestampValue` (RFC 3339 or null), `mapStrings` (a non-nil map for a known value, so an empty map still reaches the API), `writeOnceString()` (plan modifier that errors when a set value is changed or removed, for write-once API fields where replacement would be destructive), `withoutPrefix` (regexp matching strings that do not begin with a prefix, for `stringvalidator.RegexMatches` since Go regexp has no lookahead), `codeList` (renders allowed values as Markdown code spans for descriptions)
- **Validation**: use `github.com/hashicorp/terraform-plugin-framework-validators` (`stringvalidator`, `setvalidator`, `mapvalidator`) instead of custom `validator.*` types. Do not use `stringvalidator.OneOf` for values the API can extend without a breaking change (geos, models): the SDK's enums are open strings, and a closed list would block new values until a provider release. tfplugindocs does not render validator descriptions, so state the constraint in `MarkdownDescription` too
- **SDK params**: `param.Opt[T]` fields are set with `anthropic.String(...)`. Plain map and slice fields tagged `omitzero` are omitted only when nil; a non-nil empty map is sent as `{}`. Union params such as `allowed_inference_geos` set `OfUnrestricted: constant.ValueOf[constant.Unrestricted]()` or `OfGeos`
- **Value objects**: convert between the Terraform model and SDK types through a small domain type (for example `dataResidency` with `dataResidencyFromAPI`, `dataResidencyFromObject`, `objectValue`, `createParam`, `updateParam`) instead of shuffling primitives inline in CRUD methods
- **Import**: `anthropic_workspace` uses `ImportStatePassthroughID`. Compound IDs (for example a workspace member keyed by `<workspace_id>/<user_id>`) are documented in `references/guides/state-management.md`
- **Error handling**: `isNotFound(err)` in `errors.go` unwraps with `errors.As` into `*anthropic.Error` (an alias for the SDK's `apierror.Error`) and matches `StatusCode == 404`. Read removes the resource from state on a 404 or a non-zero `ArchivedAt`; Delete ignores both. API failures are reported as `"API Error"` with `Unable to <verb> <resource>: %s`
- **Destroy semantics**: the Admin API has no workspace delete, so `anthropic_workspace` Delete archives (irreversible, and it archives the workspace's API keys). Document a non-delete destroy in the schema description and README
- **Testing**: `fake_admin_api_test.go` holds `fakeAdminAPI`, one `http.Handler` with a `ServeMux` for every Admin API endpoint the provider calls, a mutex-guarded map per object type, `X-Api-Key: testAPIKey` enforcement, and Anthropic-shaped JSON errors. Extend it for new resources rather than adding another fake. `setupTestServer(t, handler)` and `setupTestClient(t, server)` in `provider_test.go` wire it up through `testAPIClient`; `testProviderConfig` is the provider block for steps. `live_test.go` holds `TestLive_*`, skipped unless `TF_ACC` and `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN` are set

---

## Adding a New Resource

1. Create `internal/provider/resource_<name>.go`
2. Define model struct(s) with `tfsdk` tags
3. Implement the resource:

```go
var (
    _ resource.Resource                = &fooResource{}
    _ resource.ResourceWithImportState = &fooResource{}
)

func newFoo() resource.Resource { return &fooResource{} }

type fooResource struct {
    client *anthropic.Client
}

type fooResourceModel struct {
    Id   types.String `tfsdk:"id"`
    Name types.String `tfsdk:"name"`
}

func (r *fooResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
    resp.TypeName = req.ProviderTypeName + "_foo"
}

func (r *fooResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
    resp.Schema = schema.Schema{
        Attributes: map[string]schema.Attribute{
            "id":   rsId(),
            "name": schema.StringAttribute{Required: true},
        },
    }
}

func (r *fooResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
    if req.ProviderData == nil {
        return
    }
    client, ok := req.ProviderData.(*anthropic.Client)
    if !ok {
        resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *anthropic.Client, got: %T", req.ProviderData))
        return
    }
    r.client = client
}
```

4. Implement Create, Read, Update, Delete (see guide: `references/guides/resource-lifecycle.md`)
5. Implement ImportState
6. Register in `provider.go`: add `newFoo` to `Resources()` return slice
7. Create `internal/provider/resource_foo_test.go` (see guide: `references/guides/testing.md`)

---

## Adding a Data Source

The provider does not define any data sources yet (`DataSources()` returns an empty slice). The shape below is a generic Plugin Framework data source, illustrative only:

```go
var _ datasource.DataSource = &fooDataSource{}

func newFooDataSource() datasource.DataSource { return &fooDataSource{} }

type fooDataSource struct {
    client *anthropic.Client
}

type fooDataSourceModel struct {
    Id   types.String `tfsdk:"id"`
    Name types.String `tfsdk:"name"`
}

func (d *fooDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
    resp.TypeName = req.ProviderTypeName + "_foo"
}

func (d *fooDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
    resp.Schema = schema.Schema{
        Attributes: map[string]schema.Attribute{
            "id":   schema.StringAttribute{Computed: true},
            "name": schema.StringAttribute{Required: true},
        },
    }
}

func (d *fooDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
    if req.ProviderData == nil {
        return
    }
    client, ok := req.ProviderData.(*anthropic.Client)
    if !ok {
        resp.Diagnostics.AddError("Unexpected DataSource Configure Type", fmt.Sprintf("Expected *anthropic.Client, got: %T", req.ProviderData))
        return
    }
    d.client = client
}

func (d *fooDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
    var data fooDataSourceModel
    resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
    if resp.Diagnostics.HasError() {
        return
    }
    // API call, populate data fields...
    resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
```

Register: add `newFooDataSource` to `DataSources()` in `provider.go`.

---

## Schema Design Quick-Reference

| Schema Type                                                                                    | Go Model Type            | When to Use                    |
| ------------------------------------------------------------------------------------------------ | ------------------------- | --------------------------------- |
| `schema.StringAttribute{Required: true}`                                                       | `types.String`           | User must provide              |
| `schema.StringAttribute{Optional: true}`                                                       | `types.String`           | User may provide               |
| `schema.StringAttribute{Computed: true}`                                                       | `types.String`           | Server-generated only          |
| `schema.StringAttribute{Optional: true, Computed: true}`                                       | `types.String`           | User provides OR server fills  |
| `schema.SetAttribute{Optional: true, Computed: true, ElementType: types.StringType}`            | `types.Set`              | Set the server fills when unset |
| `schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false)}`    | `types.Bool`          | Attribute with a known default |

### Plan Modifiers

| Modifier                                  | Use Case                                   |
| ------------------------------------------ | ------------------------------------------ |
| `stringplanmodifier.UseStateForUnknown()` | Computed value stable across updates (for example `rsId()` output, or a created-at timestamp) |
| `boolplanmodifier.RequiresReplace()`      | Changing this attribute forces resource recreation |
| `stringplanmodifier.RequiresReplaceIfConfigured()` | Recreate only when the practitioner configures a different value, for example `data_residency.workspace_geo`, so an unset value on an imported resource never replaces it |
| `objectplanmodifier.UseStateForUnknown()` | Optional + Computed nested object left unmanaged when unset, for example `data_residency` |
| `setplanmodifier.UseStateForUnknown()`    | Same idea, for a Set attribute |

Full details: `references/guides/schema-design.md`

---

## Testing Patterns

### Test Infrastructure

```go
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
    "anthropic": providerserver.NewProtocol6WithError(New("test")()),
}

const testProviderConfig = `
provider "anthropic" {
  api_key = "sk-ant-admin-unused"
}
`
```

### Test Structure

```go
func TestAccFoo_Basic(t *testing.T) {
    fake := newFakeAdminAPI()
    server := setupTestServer(t, fake)
    setupTestClient(t, server)

    resource.Test(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: testProviderConfig + `
resource "anthropic_foo" "test" {
  name = "example"
}
`,
                Check: resource.ComposeAggregateTestCheckFunc(
                    resource.TestCheckResourceAttrSet("anthropic_foo.test", "id"),
                ),
            },
        },
    })
}
```

### Running Tests

```bash
TF_ACC=1 go test ./internal/provider/ -v -run TestAcc
TF_ACC=1 go test ./internal/provider/ -v -run TestAccWorkspace
```

Full details: `references/guides/testing.md`

---

## State Upgrade

No resource has needed a state upgrade yet. If a future breaking schema change requires one (e.g., changing an attribute from a Set to a nested block):

1. Increment `Version` in the schema
2. Implement `resource.ResourceWithUpgradeState`
3. Parse raw JSON state and write to current model

```go
func (r *fooResource) UpgradeState(_ context.Context) map[int64]resource.StateUpgrader {
    return map[int64]resource.StateUpgrader{
        0: {
            StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
                var raw map[string]json.RawMessage
                if err := json.Unmarshal(req.RawState.JSON, &raw); err != nil {
                    resp.Diagnostics.AddError("State Upgrade Error", fmt.Sprintf("Unable to parse raw state: %s", err))
                    return
                }
                // Parse old format, build new model, set state
                resp.Diagnostics.Append(resp.State.Set(ctx, &newModel)...)
            },
        },
    }
}
```

Full details: `references/guides/state-management.md`

---

## Reference Docs

### Topic Guides (synthesized, task-oriented)

| Guide                                         | Contents                                              |
| ---------------------------------------------- | ------------------------------------------------------ |
| `references/guides/resource-lifecycle.md`     | CRUD methods, interface contracts, registration       |
| `references/guides/data-source-lifecycle.md`  | Data source pattern, Read method                      |
| `references/guides/schema-design.md`          | Attributes, blocks, types, nested models              |
| `references/guides/plan-modification.md`      | UseStateForUnknown, RequiresReplace, custom modifiers |
| `references/guides/state-management.md`       | Import, state upgrade, private state                  |
| `references/guides/validation.md`             | Attribute validators, resource-level validation       |
| `references/guides/testing.md`                | Acceptance tests, fake Admin API server, test steps  |
| `references/guides/provider-configuration.md` | Provider setup, client injection, servers             |
| `references/guides/functions.md`              | Provider-defined functions (Terraform 1.8+)           |

### Framework Reference (verbatim, upstream HashiCorp docs)

Key entry points in `references/framework/`:

| File                                 | Contents                         |
| ------------------------------------- | --------------------------------- |
| `resources/index.mdx`                | Resource interface, registration |
| `resources/create.mdx`               | Create method contract           |
| `resources/read.mdx`                 | Read method, refresh state       |
| `resources/update.mdx`               | Update method, in-place changes  |
| `resources/delete.mdx`               | Delete method                    |
| `resources/configure.mdx`            | Client injection into resources  |
| `resources/import.mdx`               | Import state support             |
| `resources/plan-modification.mdx`    | Plan modifiers                   |
| `resources/state-upgrade.mdx`        | State upgrade for schema changes |
| `data-sources/index.mdx`             | Data source interface            |
| `handling-data/schemas.mdx`          | Schema definition                |
| `handling-data/accessing-values.mdx` | Reading config/plan/state        |
| `handling-data/writing-state.mdx`    | Writing to response state        |
| `handling-data/attributes/index.mdx` | All attribute types              |
| `handling-data/blocks/index.mdx`     | All block types                  |
| `handling-data/types/index.mdx`      | Type system (Go value types)     |
| `validation.mdx`                     | Validation patterns              |
| `diagnostics.mdx`                    | Error/warning diagnostics        |
| `acctests.mdx`                       | Acceptance testing setup         |
| `debugging.mdx`                      | Debugging providers              |
| `providers/index.mdx`                | Provider interface               |
| `provider-servers.mdx`               | Provider server (main.go)        |
| `functions/implementation.mdx`       | Provider functions               |
| `migrating/index.mdx`                | SDKv2 migration overview         |
