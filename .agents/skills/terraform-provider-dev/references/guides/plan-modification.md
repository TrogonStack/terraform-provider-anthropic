# Plan Modification

## Overview

After validation and before apply, Terraform generates a plan describing expected values. Plan modifiers let you:

- Provide known values for computed attributes (reduce "known after apply" noise)
- Mark resources for replacement when in-place update is impossible
- Return diagnostics on planned changes

## Plan Modification Process

1. Null config values get their default value applied
2. If plan differs from state, computed attributes with null config become unknown
3. Attribute plan modifiers run (in schema order)
4. Resource-level plan modifiers run (`ModifyPlan`)

After apply, all state values MUST match planned values or Terraform produces a "Provider produced inconsistent result" error.

## Built-in Attribute Plan Modifiers

Available in `resource/schema/<type>planmodifier` packages:

### UseStateForUnknown

Copies the prior state value into the plan. Use for computed values that don't change after creation (IDs, creation timestamps), and for Optional+Computed values the practitioner may be leaving unmanaged.

```go
import "github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"

schema.StringAttribute{
    Computed: true,
    PlanModifiers: []planmodifier.String{
        stringplanmodifier.UseStateForUnknown(),
    },
}
```

No resource exists yet, but the `rsId()` helper described in `references/guides/schema-design.md` wraps exactly this pattern for `id`, and any other Computed-only or Optional+Computed attribute that's stable after creation should use the same modifier directly.

### RequiresReplace

Forces resource destruction and recreation when the attribute value changes. Use for immutable API fields.

```go
schema.BoolAttribute{
    Optional: true,
    Computed: true,
    Default:  booldefault.StaticBool(false),
    PlanModifiers: []planmodifier.Bool{
        boolplanmodifier.RequiresReplace(),
    },
}
```

Reach for this on any future attribute the Admin API has no update operation for, so changing it in configuration means a different object, not an update to the current one. Whether any Admin API resource actually has such a field isn't known yet.

### RequiresReplaceIf

Conditional replacement based on provider-defined logic:

```go
stringplanmodifier.RequiresReplaceIf(
    func(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
        // Only replace if changing from non-empty to different non-empty
        resp.RequiresReplace = !req.StateValue.IsNull() && !req.PlanValue.IsNull()
    },
    "Replace when changing between non-null values",
    "Replace when changing between non-null values",
)
```

Not used anywhere in this provider yet.

### RequiresReplaceIfConfigured

Like RequiresReplace but only triggers if the practitioner explicitly configured the value (not null):

```go
stringplanmodifier.RequiresReplaceIfConfigured()
```

`anthropic_workspace` uses it on `data_residency.workspace_geo`, paired with `UseStateForUnknown` and no default. Plain `RequiresReplace` with a static default would replace, and so archive, any imported workspace whose geo differs from the default when the practitioner leaves the attribute unset.

## Available Modifier Packages

Each type has its own package:

| Type    | Package                               | Modifiers                                                                           |
| --------- | ---------------------------------------- | ---------------------------------------------------------------------------------------- |
| String  | `resource/schema/stringplanmodifier`  | UseStateForUnknown, RequiresReplace, RequiresReplaceIf, RequiresReplaceIfConfigured |
| Bool    | `resource/schema/boolplanmodifier`    | UseStateForUnknown, RequiresReplace, RequiresReplaceIf, RequiresReplaceIfConfigured |
| Int64   | `resource/schema/int64planmodifier`   | UseStateForUnknown, RequiresReplace, RequiresReplaceIf, RequiresReplaceIfConfigured |
| Float64 | `resource/schema/float64planmodifier` | UseStateForUnknown, RequiresReplace, RequiresReplaceIf, RequiresReplaceIfConfigured |
| List    | `resource/schema/listplanmodifier`    | UseStateForUnknown, RequiresReplace, RequiresReplaceIf, RequiresReplaceIfConfigured |
| Map     | `resource/schema/mapplanmodifier`     | UseStateForUnknown, RequiresReplace, RequiresReplaceIf, RequiresReplaceIfConfigured |
| Set     | `resource/schema/setplanmodifier`     | UseStateForUnknown, RequiresReplace, RequiresReplaceIf, RequiresReplaceIfConfigured |
| Object  | `resource/schema/objectplanmodifier`  | UseStateForUnknown, RequiresReplace, RequiresReplaceIf, RequiresReplaceIfConfigured |

A Set-typed attribute (for example a set of member or key IDs on a future resource) would use `setplanmodifier.UseStateForUnknown()`, the Set-typed counterpart to the String one above, if it's Optional+Computed and meant to stay quiet on the plan when unmanaged.

## Custom Plan Modifiers

Implement the relevant `planmodifier.<Type>` interface:

```go
type myModifier struct{}

func (m myModifier) Description(_ context.Context) string {
    return "Description for practitioners"
}

func (m myModifier) MarkdownDescription(_ context.Context) string {
    return "Markdown description for practitioners"
}

func (m myModifier) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
    // Access current state, config, plan values:
    //   req.StateValue  - prior state
    //   req.ConfigValue - configuration value
    //   req.PlanValue   - current plan value
    //
    // Modify plan:
    //   resp.PlanValue = types.StringValue("new-value")
    //   resp.RequiresReplace = true
}
```

Not used anywhere in this provider yet.

## Resource-Level Plan Modification

Implement `resource.ResourceWithModifyPlan` for cross-attribute logic:

```go
func (r *fooResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
    // Access full plan, state, config
    // Can add diagnostics, mark for replacement, modify plan values

    if req.Plan.Raw.IsNull() {
        // Resource is being destroyed
        return
    }

    var plan fooResourceModel
    resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
}
```

Not used anywhere in this provider yet.

## Common Patterns Expected in This Provider

### ID attributes (stable after creation)

```go
"id": rsId() // Uses UseStateForUnknown internally
```

### Immutable fields (force recreation)

```go
"some_immutable_field": schema.BoolAttribute{
    Optional: true,
    Computed: true,
    Default:  booldefault.StaticBool(false),
    PlanModifiers: []planmodifier.Bool{
        boolplanmodifier.RequiresReplace(),
    },
}
```

### Unmanaged attributes

An Optional+Computed attribute with `UseStateForUnknown()` (or its Set-typed form) is the pattern to reach for whenever a future resource has a field the practitioner may legitimately leave unset, letting the API-assigned value stand. Guard the corresponding API call behind a check for whether the practitioner actually configured the value (`isConfigured(...)` for strings, a non-nil slice from `setStrings` for sets, as described in `references/guides/schema-design.md`), so an attribute nobody configured is never pushed to the API:

```go
if isConfigured(plan.SomeField) && plan.SomeField.ValueString() != current.SomeField {
    // push the change
}
```

`UseStateForUnknown` is what keeps the plan quiet for these attributes: without it, an unset Optional+Computed attribute would show "(known after apply)" on every plan, even though nothing is actually going to change. With it, Terraform carries the prior state value forward into the plan instead. On the following Read, the resource should always write back whatever the API currently reports, regardless of what was configured, so a value changed outside Terraform is adopted into state rather than overwritten on the next apply.

No resource exists yet to confirm any of this against real Admin API behavior; treat it as the default pattern to reach for, not a guarantee about any specific future attribute.

## Related Framework References

| File                                        | Contents                             |
| ---------------------------------------------- | ----------------------------------------- |
| `framework/resources/plan-modification.mdx` | Full plan modification documentation |
| `framework/resources/default.mdx`           | Default values (interact with plan)  |
| `framework/handling-data/schemas.mdx`       | Schema definition                    |
