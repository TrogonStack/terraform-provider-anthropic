# Provider-Defined Functions

## Overview

Provider-defined functions let practitioners call provider logic directly in HCL expressions, outside any resource or data source, since Terraform 1.8. This provider defines none today: `anthropicProvider` does not implement `provider.ProviderWithFunctions`, and there is no `Functions()` method anywhere in `provider.go`. No resource or data source exists yet either, so there's nothing concrete to base a function on. Everything below is the generic Plugin Framework shape, illustrative only.

## Function Interface

```go
type Function interface {
    Metadata(context.Context, MetadataRequest, *MetadataResponse)
    Definition(context.Context, DefinitionRequest, *DefinitionResponse)
    Run(context.Context, RunRequest, *RunResponse)
}
```

## Registration

Would require adding `provider.ProviderWithFunctions` to `anthropicProvider` and a `Functions` method:

```go
func (p *anthropicProvider) Functions(_ context.Context) []func() function.Function {
    return []func() function.Function{
        newExampleFunction,
    }
}
```

## Illustrative Example: a pure string-transform function

A provider-defined function is best suited to a pure, stateless transform practitioners would otherwise have to reimplement in HCL, not to anything that calls the Admin API (that belongs in a data source). The shape below is generic, not tied to any real attribute:

### Metadata

```go
func (f *exampleFunction) Metadata(_ context.Context, req function.MetadataRequest, resp *function.MetadataResponse) {
    resp.Name = "example_function"
}
```

### Definition

```go
func (f *exampleFunction) Definition(_ context.Context, _ function.DefinitionRequest, resp *function.DefinitionResponse) {
    resp.Definition = function.Definition{
        Summary:             "Example summary",
        MarkdownDescription: "Example description.",
        Parameters: []function.Parameter{
            function.StringParameter{
                Name:                "input",
                MarkdownDescription: "The input value.",
            },
        },
        Return: function.StringReturn{},
    }
}
```

### Run

```go
func (f *exampleFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
    var input string
    resp.Error = function.ConcatFuncErrors(resp.Error, req.Arguments.Get(ctx, &input))
    if resp.Error != nil {
        return
    }

    output := strings.ToLower(input)

    resp.Error = function.ConcatFuncErrors(resp.Error, resp.Result.Set(ctx, output))
}
```

### Usage

```hcl
output "example" {
  value = provider::anthropic::example_function("Some-Value")
}
```

## Parameter Types

| Schema Parameter Type       | Go Type         |
| ------------------------------ | ---------------- |
| `function.StringParameter`   | `string`        |
| `function.BoolParameter`     | `bool`          |
| `function.Int64Parameter`    | `int64`         |
| `function.Float64Parameter`  | `float64`       |
| `function.ListParameter`     | `[]T`           |
| `function.MapParameter`      | `map[string]T`  |
| `function.ObjectParameter`   | struct          |
| `function.DynamicParameter`  | `any`           |

No function exists in this provider yet, so no parameter type has an actual use case to point to.

## Variadic Parameters

```go
Parameters: []function.Parameter{
    function.StringParameter{Name: "separator"},
},
VariadicParameter: function.StringParameter{
    Name: "values",
},
```

Would matter for a hypothetical function joining a variable number of values, for example a set of IDs.

## Return Types

Same type set as parameters, via `function.<Type>Return{}`.

## Error Handling

```go
resp.Error = function.ConcatFuncErrors(resp.Error, function.NewArgumentFuncError(0, "input must not be empty"))
```

`function.NewArgumentFuncError(index, message)` ties an error to a specific argument position, which Terraform surfaces pointing at that argument in the calling expression.

## Testing Functions

```go
func TestExampleFunction(t *testing.T) {
    resource.UnitTest(t, resource.TestCase{
        ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
        Steps: []resource.TestStep{
            {
                Config: `
output "test" {
  value = provider::anthropic::example_function("Some-Value")
}
`,
                Check: resource.ComposeAggregateTestCheckFunc(
                    resource.TestCheckOutput("test", "some-value"),
                ),
            },
        },
    })
}
```

No such test exists in this provider, since the function itself does not exist; this mirrors the acceptance-test shape described in `references/guides/testing.md`, adapted to check a function output instead of a resource attribute.

## Related Framework References

| File                                     | Contents                   |
| ------------------------------------------- | ------------------------------- |
| `framework/functions/index.mdx`          | Function interface overview |
| `framework/functions/parameters.mdx`     | Parameter types              |
| `framework/functions/returns.mdx`        | Return types                 |
| `framework/functions/errors.mdx`         | Error handling                |
| `framework/functions/implementation.mdx` | Implementation guidance      |
