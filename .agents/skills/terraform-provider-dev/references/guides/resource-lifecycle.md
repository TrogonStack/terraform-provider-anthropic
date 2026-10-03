# Resource Lifecycle

No resource exists in this provider yet. Everything below is the generic Plugin Framework contract plus the API facts already known from `client.go`, written as a pattern to follow rather than a description of real code. Where a convention depends on the API's actual behavior (not-found mapping, destroy semantics, retry-on-conflict), this guide says so instead of guessing.

## Interface

A resource must implement `resource.Resource`:

```go
type Resource interface {
    Metadata(context.Context, MetadataRequest, *MetadataResponse)
    Schema(context.Context, SchemaRequest, *SchemaResponse)
    Create(context.Context, CreateRequest, *CreateResponse)
    Read(context.Context, ReadRequest, *ReadResponse)
    Update(context.Context, UpdateRequest, *UpdateResponse)
    Delete(context.Context, DeleteRequest, *DeleteResponse)
}
```

Optional interfaces:

- `resource.ResourceWithConfigure`: receive provider client
- `resource.ResourceWithImportState`: support `terraform import`
- `resource.ResourceWithUpgradeState`: handle schema migrations
- `resource.ResourceWithModifyPlan`: resource-level plan modification
- `resource.ResourceWithValidateConfig`: resource-level validation

Every resource in this provider will need at least `resource.Resource` and, almost certainly, `resource.ResourceWithImportState` (the Admin API's resources all have stable IDs practitioners can import by). Whether any of them need `ResourceWithUpgradeState`, `ResourceWithModifyPlan`, or `ResourceWithValidateConfig` is a per-resource decision, not a provider-wide one.

## Registration

Add a constructor function to the provider's `Resources()` method:

```go
func newFoo() resource.Resource { return &fooResource{} }

// In provider.go:
func (p *anthropicProvider) Resources(ctx context.Context) []func() resource.Resource {
    return []func() resource.Resource{
        newFoo,
    }
}
```

`Resources()` returns an empty slice today.

## Metadata

Sets the resource type name as it appears in Terraform configurations:

```go
func (r *fooResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
    resp.TypeName = req.ProviderTypeName + "_foo"
}
```

`req.ProviderTypeName` is `"anthropic"` (set in the provider's own `Metadata`), so every resource type name is `anthropic_<name>`, matching the `anthropic_` resource prefix.

## Configure

Receive the provider-configured API client:

```go
func (r *fooResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
    if req.ProviderData == nil {
        return
    }
    client, ok := req.ProviderData.(*apiClient)
    if !ok {
        resp.Diagnostics.AddError("Unexpected Resource Configure Type",
            fmt.Sprintf("Expected *apiClient, got: %T", req.ProviderData))
        return
    }
    r.client = client
}
```

The `nil` check is required: Configure is called during validation when provider data is not yet available.

## Create

Contract:

- Read plan data from `req.Plan`
- Perform the API creation call
- Set ALL attribute values (including computed) in `resp.State`
- Unknown values in plan MUST become known in state (error otherwise)
- On error, the resource is marked tainted for recreation on next plan, unless state was already written

```go
func (r *fooResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
    var plan fooResourceModel
    resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
    if resp.Diagnostics.HasError() {
        return
    }

    httpResp, err := r.client.newRequest(ctx, http.MethodPost, "/v1/organizations/foos", fooCreateBody{
        Name: plan.Name.ValueString(),
    })
    if err != nil {
        resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to create foo: %s", err))
        return
    }

    var created fooResponse
    if err := json.NewDecoder(httpResp.Body).Decode(&created); err != nil {
        resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to parse create response: %s", err))
        return
    }

    applyFoo(&plan, &created)
    resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
```

`/v1/organizations/foos`, `fooCreateBody`, and `fooResponse` are placeholders standing in for whatever the real request/response shapes turn out to be; nothing here should be read as a documented Admin API endpoint. The one fact this example does encode correctly is the call path: every mutating call goes through `r.client.newRequest`, which already attaches `anthropic-version` and the auth header (see `references/guides/provider-configuration.md`).

### Partial Create

If a resource's creation involves more than one API call (for example creating a parent object, then a follow-up call to attach a child object to it), a failure partway through leaves the first object already created with a real ID. Returning an error from Create without writing any state would leave Terraform believing creation never started, and a retried apply could collide with the object that already exists. The usual fix, seen in sibling providers as `keepPartialCreate`, is to re-read whatever was actually created and write it to state before returning the error, so the next apply operates against the real, partially-configured object instead of trying to create it again. Whether any resource here needs this shape depends on whether its Admin API create call is a single request or several.

## Read

Contract:

- Read prior state from `req.State`
- Perform the API read call
- If the resource no longer exists: call `resp.State.RemoveResource(ctx)` and return
- Otherwise, update all state values to reflect current API state

```go
func (r *fooResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
    var state fooResourceModel
    resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
    if resp.Diagnostics.HasError() {
        return
    }

    found, err := r.findFoo(ctx, state.Id.ValueString())
    if err != nil {
        resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read foo: %s", err))
        return
    }
    if found == nil {
        resp.State.RemoveResource(ctx)
        return
    }

    applyFoo(&state, found)
    resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
```

A `findFoo` helper that collapses "gone" into a single `nil, nil` result (the way sibling providers do) keeps Read to one branch, whatever the Admin API's not-found signal turns out to look like in practice.

## Not-Found Detection

The Admin API reports an error as an HTTP status code with a JSON body shaped:

```json
{"type":"error","error":{"type":"not_found_error","message":"..."}}
```

No helper for reading this shape exists yet. The actual detection rule, whether it's the HTTP status code alone, the body's `error.type` field, or both, is a decision to make once there's a real not-found response to test against, not something to guess at in this guide. Whatever shape it takes, follow the pattern sibling providers use: unwrap with `errors.As` into a typed error rather than comparing `err.Error()` strings, since a wrapped error fails a direct type assertion.

## Update

Contract:

- Read plan data from `req.Plan` (the desired new state)
- Perform the API update call
- Set state to reflect the actual post-update values
- All values in state MUST match plan values (or Terraform produces an "inconsistent result" error)

```go
func (r *fooResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
    var plan, state fooResourceModel
    resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
    resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
    if resp.Diagnostics.HasError() {
        return
    }

    if plan.Name.ValueString() != state.Name.ValueString() {
        if _, err := r.client.newRequest(ctx, http.MethodPatch, "/v1/organizations/foos/"+state.Id.ValueString(), fooUpdateBody{
            Name: plan.Name.ValueString(),
        }); err != nil {
            resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to update foo: %s", err))
            return
        }
    }

    found, err := r.findFoo(ctx, state.Id.ValueString())
    if err != nil {
        resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read updated foo: %s", err))
        return
    }
    if found == nil {
        resp.Diagnostics.AddError("API Error", "foo was updated but could not be found afterward")
        return
    }

    applyFoo(&plan, found)
    resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}
```

Guard each call behind a comparison of plan vs. state so an attribute the plan didn't actually change is never pushed to the API, and so unconfigured, Optional+Computed attributes are never overwritten (see `references/guides/plan-modification.md`).

## Delete

Contract:

- Read prior state from `req.State`
- Perform the API deletion
- If already deleted: return without error (idempotent)
- No need to modify state: framework removes it automatically on success

```go
func (r *fooResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
    var state fooResourceModel
    resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
    if resp.Diagnostics.HasError() {
        return
    }

    if _, err := r.client.newRequest(ctx, http.MethodDelete, "/v1/organizations/foos/"+state.Id.ValueString(), nil); err != nil {
        if isNotFound(err) {
            return
        }
        resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to delete foo: %s", err))
        return
    }
}
```

Whether a given Admin API resource actually supports a hard delete, or only something softer (for example disabling a workspace member rather than removing them), is undecided until that resource's real behavior is documented. Don't assume every resource's Delete looks like the example above.

## Related Framework References

| File                                | Contents                                  |
| ------------------------------------ | -------------------------------------------- |
| `framework/resources/index.mdx`     | Resource type definition, full interface  |
| `framework/resources/create.mdx`    | Create method details and caveats         |
| `framework/resources/read.mdx`      | Read method and state refresh             |
| `framework/resources/update.mdx`    | Update method and plan consistency        |
| `framework/resources/delete.mdx`    | Delete method                             |
| `framework/resources/configure.mdx` | Configure method, provider data injection |
