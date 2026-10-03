package provider

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = &anthropicProvider{}

// testAPIClient is set by tests to bypass authentication and inject a mock client.
var testAPIClient *anthropic.Client

type anthropicProvider struct {
	version string
}

type anthropicProviderModel struct {
	APIKey    types.String `tfsdk:"api_key"`
	AuthToken types.String `tfsdk:"auth_token"`
	BaseURL   types.String `tfsdk:"base_url"`
}

func (p *anthropicProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "anthropic"
	resp.Version = p.version
}

func (p *anthropicProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manage an Anthropic organization with Terraform through the Admin API.

This provider is not affiliated with or endorsed by Anthropic.

## Authentication

The provider authenticates through the official Anthropic Go SDK, so it accepts
the same credentials as the SDK and the ` + "`ant`" + ` CLI. Setting ` + "`api_key`" + ` or
` + "`auth_token`" + ` overrides everything else. Otherwise the SDK resolves, in order:

1. ` + "`ANTHROPIC_API_KEY`" + `, for an Admin API key (` + "`sk-ant-admin...`" + `)
2. ` + "`ANTHROPIC_AUTH_TOKEN`" + `, for an OAuth token with the ` + "`org:admin`" + ` scope
3. The profile named by ` + "`ANTHROPIC_PROFILE`" + `
4. Workload Identity Federation from ` + "`ANTHROPIC_FEDERATION_RULE_ID`" + `,
   ` + "`ANTHROPIC_ORGANIZATION_ID`" + ` and ` + "`ANTHROPIC_IDENTITY_TOKEN_FILE`" + `
5. The active or ` + "`default`" + ` profile

Prefer Workload Identity Federation in CI, so the pipeline never holds a
long-lived admin secret. Service account and federation endpoints accept only
an OAuth or federation token, never an Admin API key.`,
		Attributes: map[string]schema.Attribute{
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
		},
	}
}

func (p *anthropicProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data anthropicProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// In tests, skip authentication and use the injected mock client.
	if testAPIClient != nil {
		resp.DataSourceData = testAPIClient
		resp.ResourceData = testAPIClient
		return
	}

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
}

func (p *anthropicProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{}
}

func (p *anthropicProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &anthropicProvider{
			version: version,
		}
	}
}
