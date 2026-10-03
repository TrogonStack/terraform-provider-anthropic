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

No resource or data source exists yet. The provider surface will eventually cover the Anthropic Admin API's organization, workspaces, workspace members, users, invites, API keys, service accounts, and federation issuers/rules, but no schema or CRUD behavior for any of them is decided. What's already in place, in `internal/provider/`:

- **Package**: `internal/provider` (single flat package; every resource and data source will live here)
- **File naming**: `resource_<name>.go`, `resource_<name>_test.go`, `data_source_<name>.go`
- **Provider**: `anthropicProvider`. `Metadata` sets `resp.TypeName = "anthropic"`, so every resource type name is `anthropic_<name>`
- **Provider schema**: two Optional, Sensitive attributes, `api_key` (an Admin API key, `sk-ant-admin...`) and `auth_token` (an OAuth or workload-identity-federation token with the `org:admin` scope). Setting both is `errConflictingCredential`. If neither is set, the official Go SDK's own credential chain applies, in order: `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_PROFILE`, workload identity federation env vars (`ANTHROPIC_FEDERATION_RULE_ID`, `ANTHROPIC_ORGANIZATION_ID`, `ANTHROPIC_IDENTITY_TOKEN_FILE`), then the active or default profile. There is no provider-level "no credential" error; an unresolved credential surfaces as a failure on the first API call. `base_url` is Optional and falls back to the SDK's own default
- **Provider client**: the hand-rolled HTTP client is gone. The provider depends on the official Go SDK, `github.com/anthropics/anthropic-sdk-go` v1.78.0. `client.go`'s `newClient(clientConfig{apiKey, authToken, baseURL string}) (*anthropic.Client, error)` builds an `anthropic.Client` via `anthropic.NewClient(opts...)`, always passing `option.WithMaxRetries(5)`, plus `option.WithAPIKey`/`option.WithAuthToken` when a credential is configured and `option.WithBaseURL` when `base_url` is set. The SDK handles its own retry behavior; this provider carries no custom retry logic of its own
- **Client injection**: each resource and data source's `Configure` method casts `req.ProviderData.(*anthropic.Client)`
- **Registration**: `Resources()` and `DataSources()` in `provider.go` both return empty slices today. The first resource adds its constructor to `Resources()`
- **ID helper**: no `rsId()`-style helper exists yet, but sibling providers in this family define one (a Computed `StringAttribute` with `UseStateForUnknown`, see `references/guides/schema-design.md`); reach for the same shape instead of inventing a new one when the first resource lands
- **Import**: whether every resource's ID round-trips through a plain `ImportStatePassthroughID`, or whether some need compound parsing (for example a workspace member keyed by `<workspace_id>/<user_id>`), depends on which resource lands first. Both patterns are documented in `references/guides/state-management.md`
- **Error handling**: SDK calls return errors; an API failure unwraps with `errors.As` into `*anthropic.Error` (an alias for the SDK's internal `apierror.Error`), which carries `StatusCode int` and a `Type() shared.ErrorType` method parsed from the API's `{"error":{"type":"..."}}` envelope (for example `shared.ErrorTypeNotFoundError`). Whether "not found" is detected from `StatusCode`, `.Type()`, or both, is a decision for whoever implements the first resource, not something to guess at here
- **Destroy semantics**: undecided; depends on what the Admin API supports per resource (hard delete, archive, revoke) once a resource lands
- **Testing**: `testAPIClient` is a package var of type `*anthropic.Client` that tests set to inject a client pointed at an `httptest.Server`, built with `newClient(clientConfig{apiKey: "...", baseURL: server.URL})`. Whether a fake Admin API server is one handler covering every endpoint or several smaller ones is undecided until there's a resource to drive it

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

The provider does not define any data sources yet (`DataSources()` returns an empty slice), and no resource exists yet either. The shape below is a generic Plugin Framework data source, illustrative only:

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
  api_key = "sk-ant-admin-test"
}
`
```

### Test Structure

```go
func TestAccFoo_Basic(t *testing.T) {
    fake := newFakeAdminAPI() // illustrative: no fake server exists yet
    server := httptest.NewServer(fake)
    t.Cleanup(server.Close)
    testAPIClient, _ = newClient(clientConfig{apiKey: "sk-ant-admin-test", baseURL: server.URL})
    t.Cleanup(func() { testAPIClient = nil })

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
go test ./internal/provider/ -v -run TestAcc
go test ./internal/provider/ -v -run TestAccFoo
```

Full details: `references/guides/testing.md`

---

## State Upgrade

No resource exists yet, so none has needed a state upgrade. If a future breaking schema change requires one (e.g., changing an attribute from a Set to a nested block):

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
