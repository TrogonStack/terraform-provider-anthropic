# Provider Configuration

## Overview

The provider block configures shared state (API clients, credentials) passed to every resource and data source via `Configure`.

## Provider Interface

```go
type Provider interface {
    Metadata(context.Context, MetadataRequest, *MetadataResponse)
    Schema(context.Context, SchemaRequest, *SchemaResponse)
    Configure(context.Context, ConfigureRequest, *ConfigureResponse)
    Resources(context.Context) []func() resource.Resource
    DataSources(context.Context) []func() datasource.DataSource
}
```

## This Provider's Structure

### Metadata

```go
func (p *anthropicProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
    resp.TypeName = "anthropic"
    resp.Version = p.version
}
```

### Schema

Three attributes: `admin_api_key`, `auth_token`, and `base_url`. `admin_api_key` and `auth_token` are both Optional and Sensitive; exactly one of them must resolve to a value:

```go
"admin_api_key": schema.StringAttribute{
    Optional:            true,
    Sensitive:           true,
    MarkdownDescription: "The Anthropic Admin API key, sent as `x-api-key`. Exactly one of `admin_api_key` or `auth_token` must be set.",
},
"auth_token": schema.StringAttribute{
    Optional:            true,
    Sensitive:           true,
    MarkdownDescription: "An `org:admin` OAuth or workload-identity-federation token, sent as `Authorization: Bearer`. Exactly one of `admin_api_key` or `auth_token` must be set.",
},
"base_url": schema.StringAttribute{
    Optional:            true,
    MarkdownDescription: "The Admin API base URL. Defaults to `https://api.anthropic.com`.",
},
```

The provider-level `MarkdownDescription` is the place to document that these two credential attributes are mutually exclusive, since neither attribute's own schema can express "exactly one of" by itself (see `schemavalidator.ExactlyOneOf` in `references/guides/validation.md` for a declarative alternative to an error raised in `Configure`).

### Provider Model

```go
type anthropicProviderModel struct {
    AdminApiKey types.String `tfsdk:"admin_api_key"`
    AuthToken   types.String `tfsdk:"auth_token"`
    BaseUrl     types.String `tfsdk:"base_url"`
}
```

### Configure

```go
adminApiKey := data.AdminApiKey.ValueString()
if adminApiKey == "" {
    adminApiKey = os.Getenv("ANTHROPIC_ADMIN_API_KEY")
}

authToken := data.AuthToken.ValueString()
if authToken == "" {
    authToken = os.Getenv("ANTHROPIC_AUTH_TOKEN")
}

if adminApiKey != "" && authToken != "" {
    resp.Diagnostics.AddError("Configuration Error", "admin_api_key and auth_token are mutually exclusive; set only one")
    return
}
if adminApiKey == "" && authToken == "" {
    resp.Diagnostics.AddError("Configuration Error", "one of admin_api_key or auth_token must be set")
    return
}

baseUrl := data.BaseUrl.ValueString()
if baseUrl == "" {
    baseUrl = os.Getenv("ANTHROPIC_BASE_URL")
}
if baseUrl == "" {
    baseUrl = "https://api.anthropic.com"
}

client := newAPIClient(baseUrl, credentialFrom(adminApiKey, authToken), newRetryableClient())
```

The exact shape of `credentialFrom` (a small sum type carrying either the API key or the bearer token, so `newRequest` can pick the right header) is an implementation detail of `client.go`; nothing above should be read as the literal function signature, only the resolution order: explicit attribute, then environment variable, then (for `base_url` only) a hardcoded default.

A `testAPIClient != nil` branch runs before this and hands the injected client straight to resources and data sources (see `references/guides/testing.md`).

### Client Structure

```go
type apiClient struct {
    baseURL    string
    credential credential
    httpClient *http.Client
}
```

`credential` carries exactly one of the two provider attributes: `admin_api_key` is sent as the `x-api-key` header, `auth_token` as `Authorization: Bearer`. `newRequest(ctx, method, path, body)` is the single place that sets `anthropic-version: 2023-06-01` and the auth header, so every resource and data source goes through it rather than building `*http.Request` values directly.

### Client Data Flow

```
provider "anthropic" { admin_api_key = "..." }
          |
          v
   Configure()
          |
          v
   apiClient{ baseURL, credential, httpClient }
          |
    (DataSourceData / ResourceData)
          |
          v
  fooResource.Configure() -> r.client = client
          |
          v
  r.client.newRequest(ctx, http.MethodPost, "/v1/organizations/...", body)
```

### Resource and Data Source Registration

```go
func (p *anthropicProvider) Resources(ctx context.Context) []func() resource.Resource {
    return []func() resource.Resource{}
}
```

Both `Resources()` and `DataSources()` return empty slices today; see `references/guides/resource-lifecycle.md` and `references/guides/data-source-lifecycle.md` for the illustrative shapes a first resource and data source would follow.

## Resource/Data Source Configure Pattern

Every resource receives the client through its own `Configure` method:

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

`req.ProviderData == nil` happens during the provider's own schema/metadata validation passes, before `Configure` has run; returning early (rather than erroring) is correct here, since those passes don't call Create/Read/Update/Delete.

Unlike a provider where different resources need different credentials, every future resource here talks to the same Admin API through the same `*apiClient`, so there is no per-resource credential check to add in `Configure` beyond the type assertion above.

## Environment Variable Fallbacks

| Config Attribute | Environment Variable      | Attribute Type             |
| ------------------ | --------------------------- | --------------------------- |
| `admin_api_key`   | `ANTHROPIC_ADMIN_API_KEY` | Sensitive string, Optional |
| `auth_token`      | `ANTHROPIC_AUTH_TOKEN`    | Sensitive string, Optional |
| `base_url`        | `ANTHROPIC_BASE_URL`      | String, Optional (defaults to `https://api.anthropic.com`) |

## Provider Server (main.go)

```go
package main

import (
    "context"
    "log"

    "github.com/hashicorp/terraform-plugin-framework/providerserver"

    "github.com/TrogonStack/terraform-provider-anthropic/internal/provider"
)

var version = "dev"

func main() {
    err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
        Address: "registry.terraform.io/trogonstack/anthropic",
    })
    if err != nil {
        log.Fatal(err)
    }
}
```

`version` is overridden at build time via `-ldflags "-X main.version=..."` in release builds; `New(version)` threads it into `anthropicProviderModel`'s `resp.Version` in `Metadata`. The module path is `github.com/TrogonStack/terraform-provider-anthropic` (`go.mod`); the registry address is `registry.terraform.io/trogonstack/anthropic`.

## Related Framework References

| File                                        | Contents                          |
| ---------------------------------------------- | -------------------------------------- |
| `framework/providers/index.mdx`             | Provider interface                |
| `framework/providers/configure.mdx`         | Provider Configure method         |
| `framework/providers/validate-configuration.mdx` | Provider-level ValidateConfig |
| `framework/handling-data/schemas.mdx`       | Provider schema                   |
