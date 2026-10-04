package provider

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &apiKeysDataSource{}

func newAPIKeysDataSource() datasource.DataSource { return &apiKeysDataSource{} }

type apiKeysDataSource struct {
	client *anthropic.Client
}

type apiKeysDataSourceModel struct {
	Id              types.String  `tfsdk:"id"`
	WorkspaceId     types.String  `tfsdk:"workspace_id"`
	Status          types.String  `tfsdk:"status"`
	CreatedByUserId types.String  `tfsdk:"created_by_user_id"`
	APIKeys         []apiKeyModel `tfsdk:"api_keys"`
}

func (d *apiKeysDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_keys"
}

func (d *apiKeysDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists API keys in the organization through the Admin API, with optional filters.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier for this data source, derived from the configured filters.",
			},
			"workspace_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Filter by workspace ID.",
			},
			"status": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Filter by API key status: `active`, `inactive`, `archived` or `expired`.",
				Validators: []validator.String{
					stringvalidator.OneOf("active", "inactive", "archived", "expired"),
				},
			},
			"created_by_user_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Filter by the ID of the User who created the key.",
			},
			"api_keys": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The matching API keys.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "ID of the API key.",
						},
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Name of the API key.",
						},
						"status": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Status of the API key.",
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the API key was created.",
						},
						"expires_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the API key expires. Null when the key never expires.",
						},
						"partial_key_hint": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Partially redacted hint for the API key. Null when the API returns none.",
						},
						"created_by": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "The actor that created the API key. Null for a legacy, workload-identity-federated, or system-created key.",
							Attributes: map[string]schema.Attribute{
								"id": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "ID of the actor that created the API key.",
								},
								"type": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Type of the actor that created the API key: `user` or `service_account`.",
								},
							},
						},
						"principal": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "The principal the API key acts as. Null when the key is not bound to a principal.",
							Attributes: map[string]schema.Attribute{
								"type": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Principal type: `user_actor` or `service_account_actor`.",
								},
								"user_id": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "ID of the User the API key acts as. Null unless `type` is `user_actor`.",
								},
								"service_account_id": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "ID of the Service Account the API key acts as. Null unless `type` is `service_account_actor`.",
								},
							},
						},
						"scope": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Where the API key belongs: its workspace, or the organization for a principal-bound key with no workspace.",
							Attributes: map[string]schema.Attribute{
								"type": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Scope type: `organization` or `workspace`.",
								},
								"workspace_id": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "ID of the workspace the API key belongs to. Null for the `organization` scope.",
								},
							},
						},
					},
				},
			},
		},
	}
}

func (d *apiKeysDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*anthropic.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected DataSource Configure Type", fmt.Sprintf("Expected *anthropic.Client, got: %T", req.ProviderData))
		return
	}
	d.client = client
}

func (d *apiKeysDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data apiKeysDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.OrganizationAPIKeyListParams{Limit: anthropic.Int(1000)}
	if isConfigured(data.WorkspaceId) {
		params.WorkspaceID = anthropic.String(data.WorkspaceId.ValueString())
	}
	if isConfigured(data.Status) {
		params.Status = anthropic.OrganizationAPIKeyListParamsStatus(data.Status.ValueString())
	}
	if isConfigured(data.CreatedByUserId) {
		params.CreatedByUserID = anthropic.String(data.CreatedByUserId.ValueString())
	}

	keys := []apiKeyModel{}
	pager := d.client.Organization.APIKeys.ListAutoPaging(ctx, params)
	for pager.Next() {
		model, diags := apiKeyFromAPI(pager.Current())
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		keys = append(keys, model)
	}
	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to list API keys: %s", err))
		return
	}

	data.Id = types.StringValue(apiKeysDataSourceId(data.WorkspaceId.ValueString(), data.Status.ValueString(), data.CreatedByUserId.ValueString()))
	data.APIKeys = keys
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func apiKeysDataSourceId(workspaceId, status, createdByUserId string) string {
	id := "api_keys"
	if workspaceId != "" {
		id += "/workspace_id=" + workspaceId
	}
	if status != "" {
		id += "/status=" + status
	}
	if createdByUserId != "" {
		id += "/created_by_user_id=" + createdByUserId
	}
	return id
}
