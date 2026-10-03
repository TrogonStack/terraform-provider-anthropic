# Data Source Lifecycle

The provider does not define any data sources today (`DataSources()` in `provider.go` returns an empty slice), and it does not define any resources yet either. Everything below is illustrative: the pattern to follow when a data source is added, built from the generic Plugin Framework contract and the known `*anthropic.Client` shape (the official Go SDK's client, see `references/guides/provider-configuration.md`).

## Interface

A data source must implement `datasource.DataSource`:

```go
type DataSource interface {
    Metadata(context.Context, MetadataRequest, *MetadataResponse)
    Schema(context.Context, SchemaRequest, *SchemaResponse)
    Read(context.Context, ReadRequest, *ReadResponse)
}
```

Optional interfaces:

- `datasource.DataSourceWithConfigure`: receive provider client
- `datasource.DataSourceWithValidateConfig`: configuration validation

## Registration

```go
func newFooDataSource() datasource.DataSource { return &fooDataSource{} }

// In provider.go:
func (p *anthropicProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
    return []func() datasource.DataSource{
        newFooDataSource,
    }
}
```

## Metadata

```go
func (d *fooDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
    resp.TypeName = req.ProviderTypeName + "_foo"
}
```

## Schema

Data source schemas use the `datasource/schema` package (not `resource/schema`):

```go
import "github.com/hashicorp/terraform-plugin-framework/datasource/schema"

func (d *fooDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
    resp.Schema = schema.Schema{
        Attributes: map[string]schema.Attribute{
            "id":   schema.StringAttribute{Computed: true},
            "name": schema.StringAttribute{Required: true},
        },
    }
}
```

Key differences from resource schemas:

- No plan modifiers (no plan phase for data sources)
- No defaults (no apply phase)
- Attributes are either Required (lookup key) or Computed (returned value)
- Optional attributes serve as optional filter criteria

## Configure

Same pattern as resources:

```go
func (d *fooDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
    if req.ProviderData == nil {
        return
    }
    client, ok := req.ProviderData.(*anthropic.Client)
    if !ok {
        resp.Diagnostics.AddError("Unexpected DataSource Configure Type",
            fmt.Sprintf("Expected *anthropic.Client, got: %T", req.ProviderData))
        return
    }
    d.client = client
}
```

## Read

Contract:

- Read configuration from `req.Config` (the user-provided lookup criteria)
- Perform the API call to find the data
- If not found: add an error diagnostic (data sources must find their target)
- Set all attribute values in `resp.State`

```go
func (d *fooDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
    var data fooDataSourceModel
    resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
    if resp.Diagnostics.HasError() {
        return
    }

    found, err := d.client.findFooByName(ctx, data.Name.ValueString())
    if err != nil {
        resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to look up foo: %s", err))
        return
    }
    if found == nil {
        resp.Diagnostics.AddError("Not Found", fmt.Sprintf("foo named %q not found", data.Name.ValueString()))
        return
    }

    data.Id = types.StringValue(found.ID)
    resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
```

`findFooByName` is a placeholder wrapping whatever real SDK lookup a future data source needs. Not every area has a filter-by-name list call; for example `client.Organization.Workspaces.List(ctx, anthropic.OrganizationWorkspaceListParams{}, ...)` returns a `*pagination.Page[Workspace]` (also available pre-paginated via `ListAutoPaging`), with no name filter in its params, so a name-keyed data source over workspaces would need to list and filter client-side, or look up by ID instead via `client.Organization.Workspaces.Get(ctx, workspaceID)`. Don't assume a dedicated by-name lookup exists until the relevant SDK service's actual `Params` struct is checked.

## Data Sources vs Resources

| Aspect           | Resource                     | Data Source          |
| ------------------ | ------------------------------- | ----------------------- |
| Purpose          | Manage lifecycle (CRUD)      | Read-only lookup     |
| Methods          | Create, Read, Update, Delete | Read only            |
| Import           | Supported                    | N/A                  |
| Plan modifiers   | Yes                          | No                   |
| Defaults         | Yes                          | No                   |
| State management | Full lifecycle               | Refreshed every plan |
| Not found        | RemoveResource (drift)       | Error diagnostic     |

## Related Framework References

| File                                                | Contents                            |
| ------------------------------------------------------ | -------------------------------------- |
| `framework/data-sources/index.mdx`                  | Data source interface, registration |
| `framework/data-sources/configure.mdx`              | Configure method                    |
| `framework/data-sources/validate-configuration.mdx` | Validation                          |
| `framework/data-sources/timeouts.mdx`               | Timeout support                     |
