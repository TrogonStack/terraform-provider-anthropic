# Validation

## Overview

Validation catches configuration errors before apply, giving practitioners fast, actionable feedback instead of a failed API call mid-apply. Terraform Plugin Framework offers several validation layers. This provider uses `github.com/hashicorp/terraform-plugin-framework-validators` for attribute validation instead of hand-rolled `validator.*` types.

## Attribute Validators

Attached directly to a schema attribute, run during plan.

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

`anthropic_workspace` uses it on `data_residency.allowed_inference_geos`: null means unrestricted, and an empty set could never contain `default_inference_geo`.

## Common Validators by Type

### String Validators (`stringvalidator`)

| Validator                       | Purpose                     |
| ---------------------------------- | ------------------------------ |
| `LengthAtLeast(n)`              | Minimum string length        |
| `LengthAtMost(n)`               | Maximum string length        |
| `LengthBetween(min, max)`       | Length range                |
| `OneOf(values...)`              | Enum-style constraint        |
| `NoneOf(values...)`             | Exclusion constraint         |
| `RegexMatches(regex, message)`  | Pattern match                |
| `UTF8LengthAtLeast(n)`          | Minimum UTF-8 length         |

`anthropic_workspace` uses `LengthAtLeast(1)` on `name` and `RegexMatches` on `display_color` and on tag keys. It deliberately skips `OneOf` on the geo attributes: the SDK's enums are open strings, and the API can add a geo without a breaking change, so a closed list would block it until a provider release. State each constraint in `MarkdownDescription` too, because tfplugindocs does not render validator descriptions.

Go regexp has no negative lookahead. To reject a prefix, as tag keys must not begin with `anthropic`, use `stringvalidator.RegexMatches(withoutPrefix(prefix), message)` from `helpers.go` inside `mapvalidator.KeysAre`.

### Collection Validators (`setvalidator`, `listvalidator`, `mapvalidator`)

| Validator          | Purpose              |
| --------------------- | ----------------------- |
| `SizeAtLeast(n)`    | Minimum element count |
| `SizeAtMost(n)`     | Maximum element count |
| `SizeBetween(min, max)` | Element count range |
| `ValueStringsAre(...)` | Per-element string validators |
| `KeysAre(...)` (`mapvalidator`) | Per-key string validators |

`anthropic_workspace` uses `setvalidator.SizeAtLeast(1)` on `allowed_inference_geos`, and `mapvalidator.KeysAre` on `tags`.

### Numeric Validators (`int64validator`, `float64validator`)

| Validator           | Purpose          |
| --------------------- | ------------------ |
| `AtLeast(n)`        | Minimum value     |
| `AtMost(n)`         | Maximum value     |
| `Between(min, max)` | Value range       |

Not used anywhere in this provider yet; no resource has an `Int64Attribute` or `Float64Attribute` (see `references/guides/schema-design.md`).

## Conflict and Dependency Validators

Cross-attribute validators, usually attached at the schema level via `Validators` on the whole `schema.Schema`, or passed to `schemavalidator`:

```go
import "github.com/hashicorp/terraform-plugin-framework-validators/schemavalidator"

schemavalidator.ConflictsWith(path.MatchRoot("field_a"), path.MatchRoot("field_b"))
schemavalidator.AtLeastOneOf(path.MatchRoot("field_a"), path.MatchRoot("field_b"))
schemavalidator.ExactlyOneOf(path.MatchRoot("field_a"), path.MatchRoot("field_b"))
schemavalidator.RequiredTogether(path.MatchRoot("field_a"), path.MatchRoot("field_b"))
```

Not used anywhere in a resource schema yet. The provider schema itself has a related constraint: `api_key` and `auth_token` are mutually exclusive (`errConflictingCredential`), but neither is Required, since both can be left unset and resolved instead through the official Go SDK's own credential chain (environment variables, profiles, workload identity federation; see `references/guides/provider-configuration.md`). That asymmetry, conflict without a corresponding "at least one of", is checked imperatively in `newClient` today; `schemavalidator.ConflictsWith(path.MatchRoot("api_key"), path.MatchRoot("auth_token"))` is a declarative alternative worth considering if the provider schema is revisited, though `schemavalidator.ExactlyOneOf` would be wrong here, since it would reject the valid case where both are unset and the SDK's chain applies instead.

## Custom Validators

Implement the relevant `validator.<Type>` interface only when no composition of library validators expresses the rule. This provider defines none today:

```go
type notBlankValidator struct{}

func (v notBlankValidator) Description(_ context.Context) string {
    return "value must not be empty or whitespace-only"
}

func (v notBlankValidator) MarkdownDescription(_ context.Context) string {
    return "value must not be empty or whitespace-only"
}

func (v notBlankValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
    if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
        return
    }
    if strings.TrimSpace(req.ConfigValue.ValueString()) == "" {
        resp.Diagnostics.AddAttributeError(
            req.Path,
            "Invalid Value",
            "value must not be empty or whitespace-only",
        )
    }
}
```

This is a generic illustration, not a documented Admin API constraint; treat it as a hypothetical shape, not a rule this provider enforces.

## Resource-Level Validation (ValidateConfig)

For validation spanning multiple attributes, at the whole-resource level rather than a single schema node:

```go
var _ resource.ResourceWithValidateConfig = &fooResource{}

func (r *fooResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
    var config fooResourceModel
    resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
    if resp.Diagnostics.HasError() {
        return
    }

    if isConfigured(config.FieldA) && isConfigured(config.FieldB) && config.FieldA.ValueString() == config.FieldB.ValueString() {
        resp.Diagnostics.AddError(
            "Invalid Configuration",
            "field_a and field_b must not be identical",
        )
    }
}
```

No resource implements `resource.ResourceWithValidateConfig` yet; this is purely illustrative, built from the generic `isConfigured` helper described in `references/guides/schema-design.md`.

## Diagnostics

All validation reports through `diag.Diagnostics`:

```go
resp.Diagnostics.AddError("Summary", "Detail message")
resp.Diagnostics.AddAttributeError(path.Root("field"), "Summary", "Detail message")
resp.Diagnostics.AddWarning("Summary", "Detail message")
```

Guidelines:

- **Summary**: short, no punctuation, title case
- **Detail**: full sentence(s), actionable
- Attribute-level errors (`AddAttributeError`) point practitioners to the specific line in configuration
- Multiple diagnostics can accumulate before returning

The expected pattern in this provider, following sibling providers in this family, is to surface Admin API failures with `resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to ...: %s", err))` from Create, Read, Update, and Delete, after checking a not-found helper (see `references/guides/resource-lifecycle.md`). No resource exists yet to confirm this against real code.

## Validation Timing

```
Config → ValidateConfig (resource, provider, data source) → Attribute Validators → Plan
```

Validators run during `terraform plan` (and `terraform validate`), before any API calls. This is why an attribute validator like `setvalidator.SizeAtLeast(1)` would catch an invalid configuration before the corresponding API call is ever made, and why a test for it can assert the failure without the fake backend being involved at all (see `references/guides/testing.md`).

## Related Framework References

| File                                               | Contents                    |
| ------------------------------------------------------- | -------------------------------- |
| `framework/handling-data/validation/index.mdx`     | Validation overview         |
| `framework/handling-data/attributes/string.mdx`    | String attribute validators |
| `framework/resources/validate-configuration.mdx`   | Resource ValidateConfig     |
| `framework/providers/validate-configuration.mdx`   | Provider ValidateConfig     |
| `framework/diagnostics.mdx`                        | Diagnostics API              |
