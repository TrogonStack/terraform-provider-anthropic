package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &rateLimitsDataSource{}

func newRateLimitsDataSource() datasource.DataSource {
	return &rateLimitsDataSource{}
}

type rateLimitsDataSource struct {
	client *anthropic.Client
}

type rateLimitsDataSourceModel struct {
	Id         types.String     `tfsdk:"id"`
	GroupType  types.String     `tfsdk:"group_type"`
	Model      types.String     `tfsdk:"model"`
	RateLimits []rateLimitModel `tfsdk:"rate_limits"`
}

type rateLimitModel struct {
	Id          types.String `tfsdk:"id"`
	GroupType   types.String `tfsdk:"group_type"`
	GroupId     types.String `tfsdk:"group_id"`
	DisplayName types.String `tfsdk:"display_name"`
	Models      types.List   `tfsdk:"models"`
	Limits      types.Map    `tfsdk:"limits"`
}

func (d *rateLimitsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_rate_limits"
}

func (d *rateLimitsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the organization's rate-limit groups and their organization-level limits through the Admin API, with optional filters by group type and model. Use an entry's `id` as the `rate_limit_id` of `anthropic_workspace_rate_limit`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier for this data source, derived from the configured filters.",
			},
			"group_type": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Return only the groups of this type. The API documents `model_group`, `batch`, `token_count`, `files`, `skills` and `web_search`. The API returns no groups for `token_count`, `files` and `skills`.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"model": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Return only the group that contains this model. Accepts a full model name or an alias, for example `claude-haiku-4-5`. The read fails when no group of the organization contains the model.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"rate_limits": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The matching rate-limit entries, in the order the API returns them.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "ID of the rate-limit entry (`rlt_...`). This is the `rate_limit_id` of `anthropic_workspace_rate_limit`. It differs between organizations.",
						},
						"group_type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Type of the group, for example `model_group`, `batch` or `web_search`.",
						},
						"group_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "ID of the group (`rlg_...`).",
						},
						"display_name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Human-readable name of a `model_group`, for example `Claude Haiku 4.x`. Null for other group types.",
						},
						"models": schema.ListAttribute{
							Computed:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "The models in the group. Null for groups that are not model groups.",
						},
						"limits": schema.MapAttribute{
							Computed:            true,
							ElementType:         types.Int64Type,
							MarkdownDescription: "The organization's limit for each limiter type of the group, keyed by limiter type.",
						},
					},
				},
			},
		},
	}
}

func (d *rateLimitsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *rateLimitsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data rateLimitsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.OrganizationRateLimitListParams{}
	if isConfigured(data.GroupType) {
		params.GroupType = anthropic.OrganizationRateLimitListParamsGroupType(data.GroupType.ValueString())
	}
	if isConfigured(data.Model) {
		params.Model = anthropic.String(data.Model.ValueString())
	}

	rateLimits := []rateLimitModel{}
	pager := d.client.Organization.RateLimits.ListAutoPaging(ctx, params)
	for pager.Next() {
		entry := pager.Current()
		limits := make(map[string]int64, len(entry.Limits))
		for _, limit := range entry.Limits {
			limits[limit.Type] = limit.Value
		}
		model := rateLimitModel{
			Id:          types.StringValue(entry.ID),
			GroupType:   types.StringValue(entry.Group.Type),
			GroupId:     types.StringValue(entry.Group.ID),
			DisplayName: types.StringNull(),
			Models:      types.ListNull(types.StringType),
			Limits:      rateLimitMap(limits),
		}
		if entry.Group.DisplayName != "" {
			model.DisplayName = types.StringValue(entry.Group.DisplayName)
		}
		if entry.JSON.Models.Valid() {
			models, diags := types.ListValueFrom(ctx, types.StringType, entry.Models)
			resp.Diagnostics.Append(diags...)
			model.Models = models
		}
		rateLimits = append(rateLimits, model)
	}
	if err := pager.Err(); err != nil {
		if isNotFound(err) && isConfigured(data.Model) {
			resp.Diagnostics.AddError("Model Not Found",
				fmt.Sprintf("No rate-limit group of the organization contains the model %q: %s", data.Model.ValueString(), err))
			return
		}
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to list rate limits: %s", err))
		return
	}
	if resp.Diagnostics.HasError() {
		return
	}

	data.Id = types.StringValue(rateLimitsDataSourceId(data.GroupType.ValueString(), data.Model.ValueString()))
	data.RateLimits = rateLimits
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func rateLimitsDataSourceId(groupType, model string) string {
	var filters []string
	if groupType != "" {
		filters = append(filters, "group_type="+groupType)
	}
	if model != "" {
		filters = append(filters, "model="+model)
	}
	if len(filters) == 0 {
		return "rate_limits"
	}
	return "rate_limits/" + strings.Join(filters, ",")
}
