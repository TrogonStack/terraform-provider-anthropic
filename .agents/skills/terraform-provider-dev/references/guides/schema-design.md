# Schema Design

## Overview

Schemas define the shape of configuration, plan, and state data. Each attribute or block maps to a Go struct field via `tfsdk` tags.

```go
type fooResourceModel struct {
    Id      types.String `tfsdk:"id"`
    Name    types.String `tfsdk:"name"`
    Enabled types.Bool   `tfsdk:"enabled"`
    Created types.String `tfsdk:"created"`
}
```

No resource exists in this provider yet, so there is no real model struct to point to. The field names above are illustrative; a resource's actual attributes follow whatever shape the Admin API returns for that object.

## Attribute Types

### Primitives

| Schema Type               | Go Type         | Notes               |
| -------------------------- | ---------------- | --------------------- |
| `schema.StringAttribute`  | `types.String`  | UTF-8 string        |
| `schema.BoolAttribute`    | `types.Bool`    | true/false          |
| `schema.Int64Attribute`   | `types.Int64`   | 64-bit integer      |
| `schema.Int32Attribute`   | `types.Int32`   | 32-bit integer      |
| `schema.Float64Attribute` | `types.Float64` | 64-bit float        |
| `schema.Float32Attribute` | `types.Float32` | 32-bit float        |
| `schema.NumberAttribute`  | `types.Number`  | Arbitrary precision |

Which of these a real resource actually needs depends on its fields; nothing in this provider uses any of them yet.

### Collections

| Schema Type            | Go Type      | Requires      |
| ------------------------ | -------------- | --------------- |
| `schema.ListAttribute` | `types.List` | `ElementType` |
| `schema.MapAttribute`  | `types.Map`  | `ElementType` |
| `schema.SetAttribute`  | `types.Set`  | `ElementType` |

A membership-style attribute (for example a workspace member's role, or a set of IDs attached to an object) is a natural fit for `schema.SetAttribute` when order doesn't matter, following the shape sibling providers use for unordered ID collections:

```go
"member_ids": schema.SetAttribute{
    Optional:    true,
    Computed:    true,
    ElementType: types.StringType,
    MarkdownDescription: "IDs of the members. Leave unset to leave them unmanaged.",
    PlanModifiers: []planmodifier.Set{
        setplanmodifier.UseStateForUnknown(),
    },
}
```

This is illustrative; no resource in this provider defines `member_ids` or any other attribute yet.

### Nested Attributes (Protocol v6 only)

| Schema Type                    | Go Type                  | Use Case                |
| --------------------------------- | -------------------------- | -------------------------- |
| `schema.SingleNestedAttribute` | `*nestedModel`           | Single object           |
| `schema.ListNestedAttribute`   | `[]nestedModel`          | Ordered list of objects |
| `schema.MapNestedAttribute`    | `map[string]nestedModel` | Keyed objects           |
| `schema.SetNestedAttribute`    | `[]nestedModel`          | Unique set of objects   |

```go
schema.SingleNestedAttribute{
    Optional: true,
    Attributes: map[string]schema.Attribute{
        "key":   schema.StringAttribute{Required: true},
        "value": schema.StringAttribute{Required: true},
    },
}
```

## Blocks

Blocks are structural containers that appear as HCL blocks (with `{}` syntax). Use blocks for complex nested structures, especially when they can be optional or repeated.

| Schema Type                | Go Type                               | HCL Syntax                              |
| ----------------------------- | ---------------------------------------- | ------------------------------------------ |
| `schema.SingleNestedBlock` | `*nestedModel` (pointer for optional) | `block_name { ... }`                    |
| `schema.ListNestedBlock`   | `[]nestedModel`                       | `block_name { ... }` (repeated)         |
| `schema.SetNestedBlock`    | `[]nestedModel`                       | `block_name { ... }` (unique, repeated) |

### Blocks vs Nested Attributes

| Use Blocks When                                      | Use Nested Attributes When             |
| -------------------------------------------------------- | ------------------------------------------- |
| Optional complex object (pointer nil = not provided) | Always-present object structure        |
| Matching existing Terraform provider conventions     | New providers (preferred direction)    |
| HCL block syntax feels natural for the structure     | Programmatic, data-oriented structures |

Nested attributes are the preferred direction for a new provider; reach for a block only if a future resource needs an optional, HCL-block-shaped nested structure and nested attributes don't fit.

## Attribute Behaviors

### Required, Optional, Computed

| Combination                                    | Meaning                                    |
| ------------------------------------------------- | ---------------------------------------------- |
| `Required: true`                               | User must provide; error if missing        |
| `Optional: true`                               | User may provide; null if omitted          |
| `Computed: true`                               | Provider sets the value; user cannot       |
| `Optional: true, Computed: true`               | User may provide OR provider fills         |
| `Optional: true, Computed: true, Default: ...` | User may provide; known default if omitted |

An `id` attribute on any future resource will almost certainly be `Computed: true` only, following the `rsId()` pattern described below. Which of a resource's other attributes are `Required`, `Optional`, or `Optional+Computed` depends on the Admin API's own rules for that object (which fields it assigns server-side, which fields have server defaults), not something to standardize here before a resource exists.

### Sensitive

```go
schema.StringAttribute{
    Required:  true,
    Sensitive: true, // Value hidden in plan/state output
}
```

The provider's own `admin_api_key` and `auth_token` attributes (in `provider.go`) are `Sensitive: true`. Whether any resource attribute needs the same treatment (for example a freshly created API key's secret value, which the Admin API likely returns only once) is a per-resource decision.

### Deprecation

```go
schema.StringAttribute{
    Optional:           true,
    DeprecationMessage: "Use 'new_field' instead.",
}
```

Not applicable yet; no resource has shipped a first version to deprecate a field from.

## Defaults

Set a known value when the user does not provide one. Requires `Optional: true, Computed: true`.

```go
import "github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"

schema.BoolAttribute{
    Optional: true,
    Computed: true,
    Default:  booldefault.StaticBool(false),
}
```

Reach for this whenever a future resource has a boolean (or other) attribute the Admin API itself defaults to a known value when omitted from a create request.

## Plan Modifiers

Control how attribute values change during planning.

```go
import "github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
import "github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"

schema.StringAttribute{
    Computed: true,
    PlanModifiers: []planmodifier.String{
        stringplanmodifier.UseStateForUnknown(), // ID: stable after creation
    },
}

schema.BoolAttribute{
    Optional: true,
    Computed: true,
    Default:  booldefault.StaticBool(false),
    PlanModifiers: []planmodifier.Bool{
        boolplanmodifier.RequiresReplace(), // Immutable: forces recreation
    },
}
```

Full details: `references/guides/plan-modification.md`.

## Validators

Constrain acceptable values at plan time.

```go
import "github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
import "github.com/hashicorp/terraform-plugin-framework/schema/validator"

schema.SetAttribute{
    Optional:    true,
    Computed:    true,
    ElementType: types.StringType,
    Validators: []validator.Set{
        setvalidator.SizeAtLeast(1),
    },
}
```

No resource exists yet to need one, but this is the shape to reach for whenever the Admin API itself rejects an empty collection or an out-of-range value: catch it in a validator at plan time instead of surfacing it only as an apply-time API error. Full details: `references/guides/validation.md`.

## The `rsId()` Helper

No `rsId()` helper exists yet in this provider, but sibling providers in this family define the same standard ID attribute pattern:

```go
func rsId() schema.StringAttribute {
    return schema.StringAttribute{
        Computed:            true,
        MarkdownDescription: "The unique ID of this resource.",
        PlanModifiers: []planmodifier.String{
            stringplanmodifier.UseStateForUnknown(),
        },
    }
}
```

Define it once (likely in `helpers.go`) and use `"id": rsId()` in every resource schema, rather than inventing a new ID pattern per resource.

## Accessing Values from Models

```go
// Read plan/config into model
var plan fooResourceModel
resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)

// Access primitive values
id := plan.Id.ValueString()

// Check whether an Optional+Computed string was actually set by the practitioner
func isConfigured(v types.String) bool {
    return !v.IsNull() && !v.IsUnknown()
}

if isConfigured(plan.Name) {
    // the practitioner set a value; push it to the API
}

// Access set elements, sorted for stable comparisons
func setStrings(ctx context.Context, v types.Set) ([]string, diag.Diagnostics) {
    if v.IsNull() || v.IsUnknown() {
        return nil, nil
    }
    out := []string{}
    diags := v.ElementsAs(ctx, &out, false)
    sort.Strings(out)
    return out, diags
}

// Set values
plan.Id = types.StringValue("some-id")
```

`isConfigured` and `setStrings` are the kind of small helpers sibling providers in this family define once (typically in `helpers.go`) and reuse across every resource, instead of inlining `IsNull()`/`IsUnknown()` checks everywhere. Neither exists in this provider yet.

## Related Framework References

| File                                                   | Contents                              |
| ---------------------------------------------------------- | ------------------------------------------ |
| `framework/handling-data/schemas.mdx`                  | Schema definition fundamentals        |
| `framework/handling-data/attributes/index.mdx`         | All attribute types overview          |
| `framework/handling-data/attributes/string.mdx`        | String attribute details              |
| `framework/handling-data/attributes/list-nested.mdx`   | List nested attribute                 |
| `framework/handling-data/attributes/single-nested.mdx` | Single nested attribute               |
| `framework/handling-data/blocks/index.mdx`             | Block types overview                  |
| `framework/handling-data/blocks/single-nested.mdx`     | SingleNestedBlock details             |
| `framework/handling-data/types/index.mdx`              | Go value types                        |
| `framework/handling-data/accessing-values.mdx`         | Reading values from state/plan/config |
| `framework/handling-data/writing-state.mdx`            | Writing values to state               |
| `framework/resources/default.mdx`                      | Default values                        |
