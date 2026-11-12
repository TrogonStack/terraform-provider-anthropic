package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &effectiveSpendLimitsDataSource{}

var spendLimitActorAttrTypes = map[string]attr.Type{
	"type":              types.StringType,
	"user_id":           types.StringType,
	"email_address":     types.StringType,
	"name":              types.StringType,
	"deleted":           types.BoolType,
	"scoped_api_key_id": types.StringType,
}

var spendLimitScopeAttrTypes = map[string]attr.Type{
	"type":          types.StringType,
	"user_id":       types.StringType,
	"seat_tier":     types.StringType,
	"rbac_group_id": types.StringType,
	"service":       types.StringType,
	"workspace_id":  types.StringType,
}

func newEffectiveSpendLimitsDataSource() datasource.DataSource {
	return &effectiveSpendLimitsDataSource{}
}

type effectiveSpendLimitsDataSource struct {
	client *anthropic.Client
}

type effectiveSpendLimitsDataSourceModel struct {
	Id                   types.String               `tfsdk:"id"`
	UserIds              types.Set                  `tfsdk:"user_ids"`
	Periods              types.Set                  `tfsdk:"periods"`
	EffectiveSpendLimits []effectiveSpendLimitModel `tfsdk:"effective_spend_limits"`
}

type effectiveSpendLimitModel struct {
	Actor             types.Object `tfsdk:"actor"`
	Amount            types.String `tfsdk:"amount"`
	Currency          types.String `tfsdk:"currency"`
	Period            types.String `tfsdk:"period"`
	PeriodToDateSpend types.String `tfsdk:"period_to_date_spend"`
	Scope             types.Object `tfsdk:"scope"`
	Source            types.Object `tfsdk:"source"`
	SpendLimitId      types.String `tfsdk:"spend_limit_id"`
}

func (d *effectiveSpendLimitsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_effective_spend_limits"
}

func spendLimitScopeAttributes(subject string) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"type": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: fmt.Sprintf("Type of %s. The API documents `user`, `seat_tier`, `rbac_group`, `organization_service`, `organization`, `workspace` and `usage_tier`, and can add types.", subject),
		},
		"user_id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Tagged ID of the member, for a `user` type. Null otherwise.",
		},
		"seat_tier": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The seat tier, for a `seat_tier` type. Null otherwise.",
		},
		"rbac_group_id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "ID of the group, for an `rbac_group` type. Null otherwise.",
		},
		"service": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The service, for an `organization_service` type. Null otherwise.",
		},
		"workspace_id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "ID of the workspace, for a `workspace` type. Null otherwise.",
		},
	}
}

func (d *effectiveSpendLimitsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the spend limit each member of a Claude Enterprise organization resolves, with the spend so far in the current period and the scope the limit is inherited from. The API returns one entry per member and period. The Spend Limits API is available to Claude Enterprise organizations only, and the API key needs the `read:spend_limits` scope.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier for this data source, derived from the configured filters.",
			},
			"user_ids": schema.SetAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Return only these members, by tagged user ID (`user_...`), at most 100.",
				Validators: []validator.Set{
					setvalidator.SizeBetween(1, 100),
					setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1)),
				},
			},
			"periods": schema.SetAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Return only the entries for these periods, at most 3. The API documents `daily`, `weekly` and `monthly`. Leave it unset to return one entry for each period a member resolves a limit for.",
				Validators: []validator.Set{
					setvalidator.SizeBetween(1, 3),
					setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1)),
				},
			},
			"effective_spend_limits": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The effective limit of each matching member and period, in the order the API returns them.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"actor": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "The member the entry reports on.",
							Attributes: map[string]schema.Attribute{
								"type": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "`user_actor` or `scoped_api_key_actor`.",
								},
								"user_id": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Tagged ID of the user, for a `user_actor`. Null otherwise.",
								},
								"email_address": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "The user's email address. Null when the account is unavailable or deleted.",
								},
								"name": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "The user's display name. Null when the account is unavailable or deleted, or has no name.",
								},
								"deleted": schema.BoolAttribute{
									Computed:            true,
									MarkdownDescription: "Whether the user's account has been deleted, for a `user_actor`. Null otherwise.",
								},
								"scoped_api_key_id": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "ID of the scoped API key, for a `scoped_api_key_actor`. Null otherwise.",
								},
							},
						},
						"amount": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The effective cap on spend within the period, as a non-negative integer decimal string in the minor unit of `currency` (cents for USD). Null when no limit applies for the period. Another period can still cap the member.",
						},
						"currency": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "ISO 4217 code of the organization's billing currency, the unit of `amount` and `period_to_date_spend`.",
						},
						"period": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The period the entry reports the limit and the spend for.",
						},
						"period_to_date_spend": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The member's spend so far in the current period, as a non-negative decimal string in the minor unit of `currency`, with up to three decimal places. Reads `0` when the spend reading is temporarily unavailable.",
						},
						"scope": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "The scope the entry reports on.",
							Attributes:          spendLimitScopeAttributes("the scope"),
						},
						"source": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "The scope the effective limit is inherited from. A `user` source is the member's own override, which `anthropic_spend_limit` manages.",
							Attributes:          spendLimitScopeAttributes("the source scope"),
						},
						"spend_limit_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "ID of the spend limit the effective limit comes from (`spl_...`). Null when the API returns none.",
						},
					},
				},
			},
		},
	}
}

func (d *effectiveSpendLimitsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *effectiveSpendLimitsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data effectiveSpendLimitsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userIds, diags := setStrings(ctx, data.UserIds)
	resp.Diagnostics.Append(diags...)
	periods, diags := setStrings(ctx, data.Periods)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaOrganizationSpendLimitEffectiveListParams{Limit: anthropic.Int(1000)}
	if len(userIds) > 0 {
		params.UserIDs = userIds
	}
	if len(periods) > 0 {
		params.Period = periods
	}

	limits := []effectiveSpendLimitModel{}
	pager := d.client.Beta.Organization.SpendLimits.Effective.ListAutoPaging(ctx, params)
	for pager.Next() {
		model, diags := effectiveSpendLimitFromAPI(pager.Current())
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		limits = append(limits, model)
	}
	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to list effective spend limits: %s", err))
		return
	}

	data.Id = types.StringValue(effectiveSpendLimitsDataSourceId(userIds, periods))
	data.EffectiveSpendLimits = limits
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func effectiveSpendLimitFromAPI(summary anthropic.BetaSpendSummary) (effectiveSpendLimitModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	actor := summary.Actor
	deleted := types.BoolNull()
	if actor.JSON.Deleted.Valid() {
		deleted = types.BoolValue(actor.Deleted)
	}
	actorValue, actorDiags := types.ObjectValue(spendLimitActorAttrTypes, map[string]attr.Value{
		"type":              types.StringValue(actor.Type),
		"user_id":           validString(actor.JSON.UserID.Valid(), actor.UserID),
		"email_address":     validString(actor.JSON.EmailAddress.Valid(), actor.EmailAddress),
		"name":              validString(actor.JSON.Name.Valid(), actor.Name),
		"deleted":           deleted,
		"scoped_api_key_id": validString(actor.JSON.ScopedAPIKeyID.Valid(), actor.ScopedAPIKeyID),
	})
	diags.Append(actorDiags...)

	scope := summary.Scope
	scopeValue, scopeDiags := spendLimitScopeValue(scope.Type,
		validString(scope.JSON.UserID.Valid(), scope.UserID),
		validString(scope.JSON.SeatTier.Valid(), scope.SeatTier),
		validString(scope.JSON.RBACGroupID.Valid(), scope.RBACGroupID),
		validString(scope.JSON.Service.Valid(), scope.Service),
		validString(scope.JSON.WorkspaceID.Valid(), scope.WorkspaceID))
	diags.Append(scopeDiags...)

	source := summary.Source
	sourceValue, sourceDiags := spendLimitScopeValue(source.Type,
		validString(source.JSON.UserID.Valid(), source.UserID),
		validString(source.JSON.SeatTier.Valid(), source.SeatTier),
		validString(source.JSON.RBACGroupID.Valid(), source.RBACGroupID),
		validString(source.JSON.Service.Valid(), source.Service),
		validString(source.JSON.WorkspaceID.Valid(), source.WorkspaceID))
	diags.Append(sourceDiags...)

	return effectiveSpendLimitModel{
		Actor:             actorValue,
		Amount:            validString(summary.JSON.Amount.Valid(), summary.Amount),
		Currency:          types.StringValue(summary.Currency),
		Period:            types.StringValue(string(summary.Period)),
		PeriodToDateSpend: types.StringValue(summary.PeriodToDateSpend),
		Scope:             scopeValue,
		Source:            sourceValue,
		SpendLimitId:      validString(summary.JSON.SpendLimitID.Valid(), summary.SpendLimitID),
	}, diags
}

func spendLimitScopeValue(scopeType string, userId, seatTier, rbacGroupId, service, workspaceId types.String) (types.Object, diag.Diagnostics) {
	return types.ObjectValue(spendLimitScopeAttrTypes, map[string]attr.Value{
		"type":          types.StringValue(scopeType),
		"user_id":       userId,
		"seat_tier":     seatTier,
		"rbac_group_id": rbacGroupId,
		"service":       service,
		"workspace_id":  workspaceId,
	})
}

// validString maps a field the API returned as null, or left out because it
// belongs to another variant of a union, to null.
func validString(valid bool, value string) types.String {
	if !valid || value == "" {
		return types.StringNull()
	}
	return types.StringValue(value)
}

// effectiveSpendLimitsDataSourceId takes the sorted filters setStrings returns.
func effectiveSpendLimitsDataSourceId(userIds, periods []string) string {
	var filters []string
	if len(userIds) > 0 {
		filters = append(filters, "user_ids="+strings.Join(userIds, ","))
	}
	if len(periods) > 0 {
		filters = append(filters, "periods="+strings.Join(periods, ","))
	}
	if len(filters) == 0 {
		return "effective_spend_limits"
	}
	return "effective_spend_limits/" + strings.Join(filters, ";")
}
