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

Three attributes: `api_key`, `auth_token`, and `base_url`. `api_key` and `auth_token` are both Optional and Sensitive:

```go
"api_key": schema.StringAttribute{
    Optional:            true,
    Sensitive:           true,
    MarkdownDescription: "An Admin API key, e.g. `sk-ant-admin...`. Conflicts with `auth_token`.",
},
"auth_token": schema.StringAttribute{
    Optional:            true,
    Sensitive:           true,
    MarkdownDescription: "An OAuth bearer token with the `org:admin` scope. Conflicts with `api_key`.",
},
"base_url": schema.StringAttribute{
    Optional:            true,
    MarkdownDescription: "The Anthropic API base URL. Falls back to `ANTHROPIC_BASE_URL`, then `https://api.anthropic.com`.",
},
```

Setting both `api_key` and `auth_token` is an error; `schema.MarkdownDescription` on the whole provider schema documents the full resolution order (see Configure below), since it spans more than a single attribute.

### Provider Model

```go
type anthropicProviderModel struct {
    APIKey    types.String `tfsdk:"api_key"`
    AuthToken types.String `tfsdk:"auth_token"`
    BaseURL   types.String `tfsdk:"base_url"`
}
```

### Configure

```go
client, err := newClient(clientConfig{
    apiKey:    data.APIKey.ValueString(),
    authToken: data.AuthToken.ValueString(),
    baseURL:   data.BaseURL.ValueString(),
})
if err != nil {
    resp.Diagnostics.AddError("Configuration Error", err.Error())
    return
}

resp.DataSourceData = client
resp.ResourceData = client
```

`newClient` (`client.go`) is where the credential resolution actually happens:

```go
func newClient(cfg clientConfig) (*anthropic.Client, error) {
    opts := []option.RequestOption{option.WithMaxRetries(maxRetries)}
    switch {
    case cfg.apiKey != "" && cfg.authToken != "":
        return nil, errConflictingCredential
    case cfg.apiKey != "":
        opts = append(opts, option.WithAPIKey(cfg.apiKey))
    case cfg.authToken != "":
        opts = append(opts, option.WithAuthToken(cfg.authToken))
    }
    if cfg.baseURL != "" {
        opts = append(opts, option.WithBaseURL(cfg.baseURL))
    }
    client := anthropic.NewClient(opts...)
    return &client, nil
}
```

If neither `api_key` nor `auth_token` is set in configuration, `anthropic.NewClient` falls back to the official Go SDK's own credential chain rather than anything this provider implements: `ANTHROPIC_API_KEY`, then `ANTHROPIC_AUTH_TOKEN`, then the profile named by `ANTHROPIC_PROFILE`, then workload identity federation from `ANTHROPIC_FEDERATION_RULE_ID`/`ANTHROPIC_ORGANIZATION_ID`/`ANTHROPIC_IDENTITY_TOKEN_FILE`, then the active or default profile. There is no provider-level "no credential resolved" error for this case; if nothing in the chain resolves, the SDK's first outgoing request fails and that failure is what practitioners see.

A `testAPIClient != nil` branch in `Configure` runs before any of this and hands the injected client straight to resources and data sources (see `references/guides/testing.md`).

### Client Structure

The provider no longer owns its own client type. `newClient` returns `*anthropic.Client`, the official SDK's top-level client, which exposes one field per API area, e.g. `client.Organization` (stable) and `client.Beta.Organization` (beta-only features). `option.WithMaxRetries(5)` is the only retry configuration the provider sets; the SDK retries itself, there is no custom retry logic to maintain.

### Client Data Flow

```
provider "anthropic" { api_key = "..." }
          |
          v
   Configure()
          |
          v
   newClient(clientConfig{...}) -> *anthropic.Client
          |
    (DataSourceData / ResourceData)
          |
          v
  fooResource.Configure() -> r.client = client
          |
          v
  r.client.Organization.Workspaces.Get(ctx, workspaceID)
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
    client, ok := req.ProviderData.(*anthropic.Client)
    if !ok {
        resp.Diagnostics.AddError("Unexpected Resource Configure Type",
            fmt.Sprintf("Expected *anthropic.Client, got: %T", req.ProviderData))
        return
    }
    r.client = client
}
```

`req.ProviderData == nil` happens during the provider's own schema/metadata validation passes, before `Configure` has run; returning early (rather than erroring) is correct here, since those passes don't call Create/Read/Update/Delete.

Unlike a provider where different resources need different credentials, every future resource here talks to the same Admin API through the same `*anthropic.Client`, so there is no per-resource credential check to add in `Configure` beyond the type assertion above.

## Credential and Environment Variable Resolution

| Config Attribute | Resolved By                 | Notes             |
| ------------------ | ---------------------------- | --------------------------- |
| `api_key`         | `newClient`, then the SDK's own chain | Sensitive string, Optional. An Admin API key, `sk-ant-admin...` |
| `auth_token`      | `newClient`, then the SDK's own chain | Sensitive string, Optional. Conflicts with `api_key` (`errConflictingCredential`) |
| `base_url`        | `newClient`, then the SDK's own default | String, Optional |

When neither credential attribute is set, the SDK (not this provider) resolves, in order: `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `ANTHROPIC_PROFILE`, the workload identity federation env vars (`ANTHROPIC_FEDERATION_RULE_ID`, `ANTHROPIC_ORGANIZATION_ID`, `ANTHROPIC_IDENTITY_TOKEN_FILE`), then the active or default profile. There is no separate, provider-specific admin-key environment variable, and no up-front "missing credential" error; an unresolved credential fails on the first request the SDK makes.

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
