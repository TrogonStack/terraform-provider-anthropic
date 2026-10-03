package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = &anthropicProvider{}

// testAPIClient is set by tests to bypass authentication and inject a mock client.
var testAPIClient *apiClient

type anthropicProvider struct {
	version string
}

type anthropicProviderModel struct {
	AdminAPIKey types.String `tfsdk:"admin_api_key"`
	AuthToken   types.String `tfsdk:"auth_token"`
	BaseURL     types.String `tfsdk:"base_url"`
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

The provider takes exactly one credential.

` + "`auth_token`" + ` is an OAuth bearer token with the ` + "`org:admin`" + ` scope, or a
short-lived token minted through Workload Identity Federation. Prefer it in CI,
because it never needs a long-lived secret.

` + "`admin_api_key`" + ` is an Admin API key (` + "`sk-ant-admin...`" + `). Only organization
members with the admin role can create one.

## Environment variables

| Attribute       | Environment variable      |
| --------------- | ------------------------- |
| ` + "`admin_api_key`" + ` | ` + "`ANTHROPIC_ADMIN_API_KEY`" + ` |
| ` + "`auth_token`" + `    | ` + "`ANTHROPIC_AUTH_TOKEN`" + `    |
| ` + "`base_url`" + `      | ` + "`ANTHROPIC_BASE_URL`" + `      |`,
		Attributes: map[string]schema.Attribute{
			"admin_api_key": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "An Admin API key, e.g. `sk-ant-admin...`. Conflicts with `auth_token`.",
			},
			"auth_token": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "An OAuth bearer token with the `org:admin` scope. Conflicts with `admin_api_key`.",
			},
			"base_url": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The Anthropic API base URL. Defaults to `" + defaultBaseURL + "`.",
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

	apiKey := valueOrEnv(data.AdminAPIKey, "ANTHROPIC_ADMIN_API_KEY")
	token := valueOrEnv(data.AuthToken, "ANTHROPIC_AUTH_TOKEN")

	cred, err := resolveCredential(apiKey, token)
	if err != nil {
		resp.Diagnostics.AddError("Configuration Error", err.Error())
		return
	}

	baseURL := valueOrEnv(data.BaseURL, "ANTHROPIC_BASE_URL")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	client := newAPIClient(newRetryableClient(), baseURL, cred)
	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *anthropicProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{}
}

func (p *anthropicProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

func valueOrEnv(value types.String, env string) string {
	if v := value.ValueString(); v != "" {
		return v
	}
	return os.Getenv(env)
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &anthropicProvider{
			version: version,
		}
	}
}
