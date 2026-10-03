# State Management

No resource exists in this provider yet. Everything below is the generic Plugin Framework contract for import, state upgrade, and private state, written as patterns to choose from rather than a description of real code.

## Import

Import lets practitioners bring existing resources under Terraform management without recreating them.

### Simple Import (PassthroughID)

When the import ID is the same as the resource's `id` attribute:

```go
var _ resource.ResourceWithImportState = &fooResource{}

func (r *fooResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
    resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
```

Usage: `terraform import anthropic_foo.example foo_0123456789`

This is the right shape for any future resource whose `id` is just the Admin API's own assigned ID for that object (for example a workspace or a user), with nothing else to parse out of the import string. After `ImportState` sets `id`, Terraform calls Read to fill in every other attribute from the live API data.

### Compound Import (Custom Parsing)

When import needs multiple values, because the resource's `id` is built by combining more than one field, `ImportState` parses the raw import string instead of passing it straight through. This is the likely shape for a resource like a workspace member, whose identity in the Admin API is naturally `<workspace_id>/<user_id>` rather than a single assigned ID:

```go
type fooImportID struct {
    First  string
    Second string
}

func parseFooImportID(raw string) (fooImportID, error) {
    parts := strings.SplitN(raw, "/", 2)
    if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
        return fooImportID{}, fmt.Errorf("expected import ID in the format <first>/<second>, got: %q", raw)
    }
    return fooImportID{First: parts[0], Second: parts[1]}, nil
}

func (r *fooResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
    parsed, err := parseFooImportID(req.ID)
    if err != nil {
        resp.Diagnostics.AddError("Invalid Import ID", err.Error())
        return
    }
    resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
    resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("first"), parsed.First)...)
    resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("second"), parsed.Second)...)
}
```

Which resources need plain passthrough and which need compound parsing is a per-resource decision, not something to settle until each one's actual identity in the Admin API is known.

## State Upgrade

When you change a resource schema in a breaking way, existing state in `.tfstate` files won't match the new schema. State upgraders transform old state to the new format transparently. No resource exists yet, so none has needed one.

### When to Use

- Changing a list block to SingleNestedBlock
- Renaming attributes
- Changing attribute types (e.g., string to int)
- Restructuring nested objects, e.g., if a flat `Set` of IDs ever moved to a list of nested objects carrying more than just an ID

### Implementation

1. Increment `Version` in the schema:

```go
func (r *fooResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
    resp.Schema = schema.Schema{
        Version: 1, // Was 0, now 1
        // ... current schema ...
    }
}
```

2. Implement `resource.ResourceWithUpgradeState`:

```go
var _ resource.ResourceWithUpgradeState = &fooResource{}

func (r *fooResource) UpgradeState(_ context.Context) map[int64]resource.StateUpgrader {
    return map[int64]resource.StateUpgrader{
        0: {
            StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
                // Parse raw JSON from old state format
                var raw map[string]json.RawMessage
                if err := json.Unmarshal(req.RawState.JSON, &raw); err != nil {
                    resp.Diagnostics.AddError("State Upgrade Error",
                        fmt.Sprintf("Unable to parse raw state: %s", err))
                    return
                }

                var id string
                _ = json.Unmarshal(raw["id"], &id)

                state := fooResourceModel{
                    Id: types.StringValue(id),
                }
                resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
            },
        },
    }
}
```

### Key Points

- The map key is the OLD schema version (upgrade FROM version X)
- `req.RawState.JSON` contains the raw JSON bytes of the old state
- Parse manually: the old state shape does not match your current model struct
- After upgrade, Terraform calls Read to refresh state with current API data
- Multiple upgraders can be chained (0->1, 1->2, etc.)

## Private State

Store provider-internal data that is not visible in plan output. Useful for:

- ETags or version tokens for optimistic concurrency
- Internal identifiers that shouldn't be user-visible
- Cached metadata to avoid extra API calls

Not used anywhere in this provider today; no resource exists yet, and whether any Admin API response carries an ETag or version token worth stashing privately isn't known. The pattern, if it's ever needed:

```go
var _ resource.ResourceWithPrivateState = &fooResource{}

// In Create or Update:
resp.Private.SetKey(ctx, "etag", []byte(apiResponse.Etag))

// In Read or Update:
etagBytes, diags := req.Private.GetKey(ctx, "etag")
etag := string(etagBytes)
```

## Writing State

### Full Model Write

Most common: write the entire model struct to state:

```go
resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
```

Every CRUD method on a resource ends this way (except Delete, which relies on the framework removing the resource automatically).

### Individual Attribute Write

Set a single attribute by path:

```go
resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("some_attribute"), value)...)
```

A plain-passthrough `ImportState` never needs this, since `ImportStatePassthroughID` sets only `id` and lets the framework's automatic follow-up Read populate everything else. A resource with a compound ID, as shown in "Compound Import" above, does need it, to seed every ID-derived field before that follow-up Read runs.

### Removing Resource from State

When Read discovers the resource no longer exists:

```go
resp.State.RemoveResource(ctx)
```

This tells Terraform the resource was deleted (or otherwise made unavailable) externally and needs recreation on the next apply. See `references/guides/resource-lifecycle.md` for how Read's `find*` helper is expected to signal "gone".

## Related Framework References

| File                                           | Contents                          |
| --------------------------------------------------- | -------------------------------------- |
| `framework/resources/import.mdx`               | Import state documentation        |
| `framework/resources/state-upgrade.mdx`        | State upgrade details             |
| `framework/resources/private-state.mdx`        | Private state storage             |
| `framework/resources/state-move.mdx`           | State move between resource types |
| `framework/handling-data/writing-state.mdx`    | Writing to response state         |
| `framework/handling-data/accessing-values.mdx` | Reading from state/plan/config    |
