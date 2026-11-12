package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &pluginsDataSource{}

func newPluginsDataSource() datasource.DataSource { return &pluginsDataSource{} }

type pluginsDataSource struct {
	client *anthropic.Client
}

type pluginsDataSourceModel struct {
	Id            types.String  `tfsdk:"id"`
	MarketplaceId types.String  `tfsdk:"marketplace_id"`
	OwnerType     types.String  `tfsdk:"owner_type"`
	OwnerUserId   types.String  `tfsdk:"owner_user_id"`
	CreatedAtGt   types.String  `tfsdk:"created_at_gt"`
	CreatedAtGte  types.String  `tfsdk:"created_at_gte"`
	CreatedAtLt   types.String  `tfsdk:"created_at_lt"`
	CreatedAtLte  types.String  `tfsdk:"created_at_lte"`
	Plugins       []pluginModel `tfsdk:"plugins"`
}

type pluginModel struct {
	Id                                          types.String            `tfsdk:"id"`
	Name                                        types.String            `tfsdk:"name"`
	DisplayName                                 types.String            `tfsdk:"display_name"`
	Description                                 types.String            `tfsdk:"description"`
	MarketplaceId                               types.String            `tfsdk:"marketplace_id"`
	Owner                                       *pluginOwnerModel       `tfsdk:"owner"`
	CreatedBy                                   *pluginCreatedByModel   `tfsdk:"created_by"`
	Components                                  []pluginComponentModel  `tfsdk:"components"`
	ContentScan                                 *pluginContentScanModel `tfsdk:"content_scan"`
	LatestVersionId                             types.String            `tfsdk:"latest_version_id"`
	ServedVersionId                             types.String            `tfsdk:"served_version_id"`
	ServedVersionPinned                         types.Bool              `tfsdk:"served_version_pinned"`
	ManifestVersion                             types.String            `tfsdk:"manifest_version"`
	Reach                                       types.String            `tfsdk:"reach"`
	OrganizationInstallationPreference          types.String            `tfsdk:"organization_installation_preference"`
	OrganizationInstallationPreferenceInherited types.Bool              `tfsdk:"organization_installation_preference_inherited"`
	CreatedAt                                   types.String            `tfsdk:"created_at"`
	UpdatedAt                                   types.String            `tfsdk:"updated_at"`
}

type pluginCreatedByModel struct {
	Type         types.String `tfsdk:"type"`
	UserId       types.String `tfsdk:"user_id"`
	EmailAddress types.String `tfsdk:"email_address"`
	APIKeyId     types.String `tfsdk:"api_key_id"`
}

type pluginComponentModel struct {
	Type        types.String `tfsdk:"type"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
}

type pluginContentScanModel struct {
	Status     types.String `tfsdk:"status"`
	Assessment types.String `tfsdk:"assessment"`
	Reason     types.String `tfsdk:"reason"`
}

func pluginFromAPI(plugin anthropic.BetaPlugin) pluginModel {
	model := pluginModel{
		Id:                                 types.StringValue(plugin.ID),
		Name:                               types.StringValue(plugin.Name),
		DisplayName:                        types.StringValue(plugin.DisplayName),
		Description:                        optionalString(plugin.Description),
		MarketplaceId:                      types.StringValue(plugin.MarketplaceID),
		Owner:                              pluginOwner(plugin.Owner.Type, plugin.Owner.UserID),
		LatestVersionId:                    types.StringValue(plugin.LatestVersionID),
		ServedVersionId:                    types.StringValue(plugin.ServedVersionID),
		ServedVersionPinned:                types.BoolValue(plugin.ServedVersionPinned),
		ManifestVersion:                    optionalString(plugin.ManifestVersion),
		Reach:                              optionalString(string(plugin.Reach)),
		OrganizationInstallationPreference: types.StringNull(),
		OrganizationInstallationPreferenceInherited: types.BoolNull(),
		CreatedAt: timestampValue(plugin.CreatedAt),
		UpdatedAt: timestampValue(plugin.UpdatedAt),
	}
	if plugin.JSON.OrganizationInstallationPreference.Valid() {
		model.OrganizationInstallationPreference = types.StringValue(string(plugin.OrganizationInstallationPreference))
	}
	if plugin.JSON.OrganizationInstallationPreferenceInherited.Valid() {
		model.OrganizationInstallationPreferenceInherited = types.BoolValue(plugin.OrganizationInstallationPreferenceInherited)
	}
	if plugin.CreatedBy.JSON.Type.Valid() {
		createdBy := plugin.CreatedBy
		model.CreatedBy = &pluginCreatedByModel{
			Type:         types.StringValue(createdBy.Type),
			UserId:       optionalString(createdBy.UserID),
			EmailAddress: optionalString(createdBy.EmailAddress),
			APIKeyId:     optionalString(createdBy.APIKeyID),
		}
	}
	if plugin.JSON.Components.Valid() {
		model.Components = make([]pluginComponentModel, 0, len(plugin.Components))
		for _, component := range plugin.Components {
			model.Components = append(model.Components, pluginComponentModel{
				Type:        types.StringValue(string(component.Type)),
				Name:        types.StringValue(component.Name),
				Description: optionalString(component.Description),
			})
		}
	}
	if plugin.JSON.ContentScan.Valid() {
		model.ContentScan = &pluginContentScanModel{
			Status:     types.StringValue(string(plugin.ContentScan.Status)),
			Assessment: optionalString(string(plugin.ContentScan.Assessment)),
			Reason:     optionalString(plugin.ContentScan.Reason),
		}
	}
	return model
}

func (d *pluginsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugins"
}

func (d *pluginsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	createdAtFilter := func(operator string) schema.StringAttribute {
		return schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: fmt.Sprintf("Only plugins created %s this RFC 3339 timestamp.", operator),
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: `Lists the organization's plugins through the Plugins API beta ` + "`ce-plugins-2026-09-01`" + `, with optional filters.

The Plugins API is available only to Claude Enterprise organizations. It
accepts an API key created in claude.ai under Organization settings > API
(` + "`sk-ant-api01-...`" + `) with the ` + "`read:plugins`" + ` scope, not an Admin API key.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier for this data source, derived from the configured filters.",
			},
			"marketplace_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Only plugins in this marketplace. An ID that matches no marketplace returns no plugins.",
			},
			"owner_type": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Only plugins with this owner type: `organization` or `user`.",
				Validators: []validator.String{
					stringvalidator.OneOf("organization", "user"),
				},
			},
			"owner_user_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Only plugins owned by this member.",
			},
			"created_at_gt":  createdAtFilter("after"),
			"created_at_gte": createdAtFilter("at or after"),
			"created_at_lt":  createdAtFilter("before"),
			"created_at_lte": createdAtFilter("at or before"),
			"plugins": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The matching plugins, newest first.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "ID of the plugin.",
						},
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Lowercase identifier, unique within its marketplace.",
						},
						"display_name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The served version's display name.",
						},
						"description": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The served version's description. Null when empty.",
						},
						"marketplace_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "ID of the marketplace the plugin lives in.",
						},
						"owner": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Who owns the plugin: the organization, or the member whose personal marketplace it lives in.",
							Attributes: map[string]schema.Attribute{
								"type": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Owner type: `organization` or `user`.",
								},
								"user_id": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "ID of the member who owns the plugin. Null unless `type` is `user`.",
								},
							},
						},
						"created_by": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Who created the plugin. Null when no creator is recorded.",
							Attributes: map[string]schema.Attribute{
								"type": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Creator type: `user_actor` or `api_actor`.",
								},
								"user_id": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "ID of the member who created the plugin. Null unless `type` is `user_actor`.",
								},
								"email_address": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Email address of the member who created the plugin. Null unless `type` is `user_actor`.",
								},
								"api_key_id": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "ID of the API key that created the plugin. Null unless `type` is `api_actor`.",
								},
							},
						},
						"components": schema.ListNestedAttribute{
							Computed:            true,
							MarkdownDescription: "What the served version contains. Null when not enumerated.",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"type": schema.StringAttribute{
										Computed:            true,
										MarkdownDescription: "Component type, for example `skill` or `mcp_server`.",
									},
									"name": schema.StringAttribute{
										Computed:            true,
										MarkdownDescription: "Name of the component.",
									},
									"description": schema.StringAttribute{
										Computed:            true,
										MarkdownDescription: "Description of the component. Null when empty.",
									},
								},
							},
						},
						"content_scan": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "The served version's content scan. Null when it has not been scanned.",
							Attributes: map[string]schema.Attribute{
								"status": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Status of the scan.",
								},
								"assessment": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Assessment of the scan. Null when the API returns none.",
								},
								"reason": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Reason for the assessment. Null when the API returns none.",
								},
							},
						},
						"latest_version_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "ID of the newest version.",
						},
						"served_version_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "ID of the version claude.ai serves to members.",
						},
						"served_version_pinned": schema.BoolAttribute{
							Computed:            true,
							MarkdownDescription: "False while the served version follows each new version; true once it has been pinned to one.",
						},
						"manifest_version": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The served version's manifest `version`, normalized for display. Null when it has none.",
						},
						"reach": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "How far the served version reaches: `contained`, `privileged` or `remote`. Null when not classified.",
						},
						"organization_installation_preference": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The organization-wide installation setting of an organization-owned plugin: its own setting, or its marketplace's default. Null for a member-owned plugin.",
						},
						"organization_installation_preference_inherited": schema.BoolAttribute{
							Computed:            true,
							MarkdownDescription: "True while `organization_installation_preference` is the marketplace's default. Null for a member-owned plugin.",
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the plugin was created.",
						},
						"updated_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of the last new version or served-version change. Installation settings do not move it.",
						},
					},
				},
			},
		},
	}
}

func (d *pluginsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *pluginsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data pluginsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaOrganizationPluginListParams{Limit: anthropic.Int(100)}
	if isConfigured(data.MarketplaceId) {
		params.MarketplaceID = anthropic.String(data.MarketplaceId.ValueString())
	}
	if isConfigured(data.OwnerType) {
		params.OwnerType = anthropic.BetaOrganizationPluginListParamsOwnerType(data.OwnerType.ValueString())
	}
	if isConfigured(data.OwnerUserId) {
		params.OwnerUserID = anthropic.String(data.OwnerUserId.ValueString())
	}
	bounds := []struct {
		name  string
		value types.String
		set   func(time.Time)
	}{
		{"created_at_gt", data.CreatedAtGt, func(t time.Time) { params.CreatedAtGt = anthropic.Time(t) }},
		{"created_at_gte", data.CreatedAtGte, func(t time.Time) { params.CreatedAtGte = anthropic.Time(t) }},
		{"created_at_lt", data.CreatedAtLt, func(t time.Time) { params.CreatedAtLt = anthropic.Time(t) }},
		{"created_at_lte", data.CreatedAtLte, func(t time.Time) { params.CreatedAtLte = anthropic.Time(t) }},
	}
	for _, bound := range bounds {
		if !isConfigured(bound.value) {
			continue
		}
		at, err := time.Parse(time.RFC3339, bound.value.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root(bound.name), "Invalid Timestamp", fmt.Sprintf("%s must be an RFC 3339 timestamp: %s", bound.name, err))
			return
		}
		bound.set(at)
	}

	plugins := []pluginModel{}
	pager := d.client.Beta.Organization.Plugins.ListAutoPaging(ctx, params)
	for pager.Next() {
		plugins = append(plugins, pluginFromAPI(pager.Current()))
	}
	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to list plugins: %s", err))
		return
	}

	data.Id = types.StringValue(pluginsDataSourceId(data))
	data.Plugins = plugins
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func pluginsDataSourceId(data pluginsDataSourceModel) string {
	id := "plugins"
	for _, filter := range []struct {
		name  string
		value types.String
	}{
		{"marketplace_id", data.MarketplaceId},
		{"owner_type", data.OwnerType},
		{"owner_user_id", data.OwnerUserId},
		{"created_at_gt", data.CreatedAtGt},
		{"created_at_gte", data.CreatedAtGte},
		{"created_at_lt", data.CreatedAtLt},
		{"created_at_lte", data.CreatedAtLte},
	} {
		if value := filter.value.ValueString(); value != "" {
			id += "/" + filter.name + "=" + value
		}
	}
	return id
}
