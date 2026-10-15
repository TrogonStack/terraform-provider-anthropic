package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &telemetryDestinationsDataSource{}

func newTelemetryDestinationsDataSource() datasource.DataSource {
	return &telemetryDestinationsDataSource{}
}

type telemetryDestinationsDataSource struct {
	client *anthropic.Client
}

type telemetryDestinationsDataSourceModel struct {
	Id                    types.String                `tfsdk:"id"`
	WorkspaceIds          types.Set                   `tfsdk:"workspace_ids"`
	TelemetryDestinations []telemetryDestinationModel `tfsdk:"telemetry_destinations"`
}

func (d *telemetryDestinationsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_telemetry_destinations"
}

func (d *telemetryDestinationsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the organization's OpenTelemetry telemetry destinations through the Admin API, newest first, with an optional filter by the workspaces they cover. This uses the Admin API beta `telemetry-destinations-2026-08-11`. Header values are write-only in the API, so only header names are exposed.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier for this data source, derived from the configured filters.",
			},
			"workspace_ids": schema.SetAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Return only the destinations that cover at least one of these workspaces (`wrkspc_...`), at most 100. A destination covers a workspace when its `workspaces.type` is `all`, when its `include` list has the workspace, or when its `exclude` list does not. Destinations in either state can match. An ID that is not one of the organization's workspaces matches no destination.",
				Validators: []validator.Set{
					setvalidator.SizeBetween(1, 100),
				},
			},
			"telemetry_destinations": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The matching telemetry destinations.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "ID of the telemetry destination (`tdst_...`).",
						},
						"endpoint_url": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The base URL of the destination's OTLP/HTTP endpoint, exactly as registered.",
						},
						"display_name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Human-readable name of the destination. Null when none was set.",
						},
						"header_names": schema.SetAttribute{
							Computed:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "Names of the request headers registered on the destination. Values are never returned.",
						},
						"enabled": schema.BoolAttribute{
							Computed:            true,
							MarkdownDescription: "Whether the destination receives deliveries.",
						},
						"disabled_reason": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Why the destination is disabled: `caller`, `sustained_failure`, `invalid_address` or `redirect`. Null while the destination is enabled.",
						},
						"traces": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "The destination's settings for the traces it receives.",
							Attributes: map[string]schema.Attribute{
								"content_mode": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "What the exported spans contain: `metadata_only` or `content`.",
								},
								"enabled": schema.BoolAttribute{
									Computed:            true,
									MarkdownDescription: "Whether the destination receives traces.",
								},
								"endpoint_url": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "The full URL the API posts traces to. Null when the API posts to the destination's `endpoint_url` with `/v1/traces` appended.",
								},
								"max_content_bytes": schema.Int64Attribute{
									Computed:            true,
									MarkdownDescription: "The most bytes of content one exported span can contain. Null when only the API's maximum applies.",
								},
							},
						},
						"workspaces": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Which of the organization's workspaces the destination covers.",
							Attributes: map[string]schema.Attribute{
								"type": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "`all`, `include` or `exclude`.",
								},
								"workspace_ids": schema.SetAttribute{
									Computed:            true,
									ElementType:         types.StringType,
									MarkdownDescription: "IDs of the workspaces on the destination's list. Null for `all` and for an empty list.",
								},
							},
						},
						"delivery_health": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "The most recent delivery results the API records for the destination.",
							Attributes: map[string]schema.Attribute{
								"last_error": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "A message describing the most recent failed delivery. Null until a delivery first fails.",
								},
								"last_error_at": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "RFC 3339 timestamp of the failure `last_error` describes. Null until a delivery first fails.",
								},
								"last_success_at": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "RFC 3339 timestamp of the most recent successful delivery. Null until a delivery first succeeds.",
								},
							},
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the destination was created.",
						},
						"updated_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of the last change to the destination's configuration or state.",
						},
					},
				},
			},
		},
	}
}

func (d *telemetryDestinationsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *telemetryDestinationsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data telemetryDestinationsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceIds, diags := setStrings(ctx, data.WorkspaceIds)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaOrganizationTelemetryDestinationListParams{Limit: anthropic.Int(1000)}
	if len(workspaceIds) > 0 {
		params.WorkspaceIDs = workspaceIds
	}

	destinations := []telemetryDestinationModel{}
	pager := d.client.Beta.Organization.TelemetryDestinations.ListAutoPaging(ctx, params)
	for pager.Next() {
		dest := pager.Current()
		var ids []string
		if dest.Workspaces.Type != "all" {
			var err error
			ids, err = listTelemetryDestinationWorkspaceIDs(ctx, d.client, dest.ID)
			if err != nil {
				resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to list the workspaces of telemetry destination %s: %s", dest.ID, err))
				return
			}
		}
		model, diags := telemetryDestinationFromAPI(ctx, dest, ids)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		destinations = append(destinations, model)
	}
	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to list telemetry destinations: %s", err))
		return
	}

	data.Id = types.StringValue(telemetryDestinationsDataSourceId(workspaceIds))
	data.TelemetryDestinations = destinations
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func telemetryDestinationsDataSourceId(workspaceIds []string) string {
	id := "telemetry_destinations"
	if len(workspaceIds) > 0 {
		sorted := append([]string{}, workspaceIds...)
		sort.Strings(sorted)
		id += "/workspace_ids=" + strings.Join(sorted, ",")
	}
	return id
}
