package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &spendLimitResource{}
	_ resource.ResourceWithImportState = &spendLimitResource{}
)

var spendLimitAmountPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

func newSpendLimit() resource.Resource { return &spendLimitResource{} }

type spendLimitResource struct {
	client *anthropic.Client
}

type spendLimitResourceModel struct {
	Id        types.String `tfsdk:"id"`
	UserId    types.String `tfsdk:"user_id"`
	Period    types.String `tfsdk:"period"`
	Amount    types.String `tfsdk:"amount"`
	Currency  types.String `tfsdk:"currency"`
	CreatedAt types.String `tfsdk:"created_at"`
	UpdatedAt types.String `tfsdk:"updated_at"`
}

func (r *spendLimitResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_spend_limit"
}

func (r *spendLimitResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a member's spend limit override in a Claude Enterprise organization through the Spend Limits API.

The Spend Limits API is available to Claude Enterprise organizations with usage
credits turned on. The API key needs the ` + "`read:spend_limits`" + ` and
` + "`write:spend_limits`" + ` scopes.

The API identifies a limit by its user and period, and setting a limit that already
exists overwrites it in place. Creating this resource therefore adopts any
override the member already has for the period, and destroying it deletes the
override. The member then falls back to the seat tier, group or organization
default for the period, or to no limit. Those defaults are configured in
claude.ai and are not available through the API. Manage each user and period
pair with one resource only.

Setting a limit does not resolve a pending increase request. Use the
` + "`anthropic_effective_spend_limits`" + ` data source to see the limit each
member resolves and where it is inherited from.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ID of the spend limit (`spl_...`).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"user_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Tagged ID of the member the limit applies to (`user_...`).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"period": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Length of the window the limit resets over. The API documents `daily`, `weekly` and `monthly`, and currently supports only `monthly`. The API applies its default when this is not set.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"amount": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The cap on spend within each period, as a non-negative integer decimal string in the minor unit of the organization's billing currency (cents for USD): `\"50000\"` is $500.00. Leave it unset to set an explicit no-limit override for the period, which differs from having no override at all.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(spendLimitAmountPattern, "must be a non-negative integer without leading zeros, in the minor unit of the billing currency"),
				},
			},
			"currency": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ISO 4217 code of the organization's billing currency, the unit of `amount`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the spend limit was created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of the last change to the spend limit.",
			},
		},
	}
}

func (r *spendLimitResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*anthropic.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *anthropic.Client, got: %T", req.ProviderData))
		return
	}
	r.client = client
}

func (r *spendLimitResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan spendLimitResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *spendLimitResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state spendLimitResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	limit, err := r.client.Beta.Organization.SpendLimits.Get(ctx, state.Id.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read spend limit %s: %s", state.Id.ValueString(), err))
		return
	}
	resp.Diagnostics.Append(spendLimitFromAPI(limit, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *spendLimitResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan spendLimitResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *spendLimitResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state spendLimitResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.client.Beta.Organization.SpendLimits.Delete(ctx, state.Id.ValueString())
	if err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to delete spend limit %s: %s", state.Id.ValueString(), err))
	}
}

func (r *spendLimitResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *spendLimitResource) set(ctx context.Context, plan *spendLimitResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	params := anthropic.BetaOrganizationSpendLimitSetParams{
		Amount: param.Null[string](),
		Scope: anthropic.BetaOrganizationSpendLimitSetParamsScopeUnion{
			OfUser: &anthropic.BetaSpendLimitUserScopeParam{UserID: plan.UserId.ValueString()},
		},
	}
	if isConfigured(plan.Amount) {
		params.Amount = anthropic.String(plan.Amount.ValueString())
	}
	if isConfigured(plan.Period) {
		params.Period = anthropic.BetaSpendLimitPeriod(plan.Period.ValueString())
	}

	limit, err := r.client.Beta.Organization.SpendLimits.Set(ctx, params)
	if err != nil {
		diags.AddError("API Error", fmt.Sprintf("Unable to set the spend limit of user %s: %s", plan.UserId.ValueString(), err))
		return diags
	}
	if isConfigured(plan.Id) && plan.Id.ValueString() != limit.ID {
		diags.AddError("Unexpected API Response",
			fmt.Sprintf("The spend limit of user %s was expected to be overwritten in place as %s, but the API returned %s.", plan.UserId.ValueString(), plan.Id.ValueString(), limit.ID))
		return diags
	}
	return spendLimitFromAPI(limit, plan)
}

// spendLimitFromAPI rejects a scope other than user, which importing a seat
// tier, group or organization limit would bring in.
func spendLimitFromAPI(limit *anthropic.BetaSpendLimit, model *spendLimitResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if limit.Scope.Type != "user" {
		diags.AddError("Unsupported Spend Limit Scope",
			fmt.Sprintf("Spend limit %s applies to a %s scope. This resource manages only user spend limits.", limit.ID, limit.Scope.Type))
		return diags
	}
	model.Id = types.StringValue(limit.ID)
	model.UserId = types.StringValue(limit.Scope.UserID)
	model.Period = types.StringValue(string(limit.Period))
	model.Amount = types.StringNull()
	if limit.JSON.Amount.Valid() {
		model.Amount = types.StringValue(limit.Amount)
	}
	model.Currency = types.StringValue(limit.Currency)
	model.CreatedAt = timestampValue(limit.CreatedAt)
	model.UpdatedAt = timestampValue(limit.UpdatedAt)
	return diags
}
