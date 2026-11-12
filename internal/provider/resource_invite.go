package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &inviteResource{}
	_ resource.ResourceWithImportState = &inviteResource{}
	_ resource.ResourceWithModifyPlan  = &inviteResource{}
)

func newInvite() resource.Resource { return &inviteResource{} }

type inviteResource struct {
	client *anthropic.Client
}

type inviteResourceModel struct {
	Id           types.String `tfsdk:"id"`
	Email        types.String `tfsdk:"email"`
	Role         types.String `tfsdk:"role"`
	RBACGroupIds types.Set    `tfsdk:"rbac_group_ids"`
	Status       types.String `tfsdk:"status"`
	InvitedAt    types.String `tfsdk:"invited_at"`
	ExpiresAt    types.String `tfsdk:"expires_at"`
	AcceptedAt   types.String `tfsdk:"accepted_at"`
}

func (r *inviteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_invite"
}

func (r *inviteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Invites a person to the organization by email through the Admin API.

Creating the invite sends the invitation email. An invite cannot be edited, so
changing any argument withdraws the invite and sends a new one. A change to
the letter case of ` + "`email`" + ` only updates state. An invite
expires 21 days after it is sent; an expired or withdrawn invite is removed
from state, so the next apply sends a new one. An accepted invite stays in
state with ` + "`status = \"accepted\"`" + `; manage the new member with ` + "`anthropic_user`" + `.
A plan that changes an accepted invite fails, because the API rejects a new
invite for a member.

Destroying this resource withdraws a pending invite. It leaves an accepted or
expired invite alone, because the API cannot withdraw one, and it never
removes a member who accepted.

Needs ` + "`read:members`" + ` and ` + "`write:members`" + ` on a Claude Enterprise key, plus
` + "`write:rbac_groups`" + ` for the ` + "`managed`" + ` role or a non-empty ` + "`rbac_group_ids`" + `.`,
		Attributes: map[string]schema.Attribute{
			"id": rsId(),
			"email": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Email of the person to invite. The API rejects an email that already has a pending invite or belongs to a member. The API compares emails without letter case, so a case-only change keeps the invite.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIf(
						emailChangedIgnoringCase,
						"Replaces the invite unless only the letter case changes.",
						"Replaces the invite unless only the letter case changes.",
					),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"role": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Organization role the person gets on accepting. A Claude Console organization accepts `user`, `developer`, `billing` and `claude_code_user`, and a Claude Enterprise organization accepts `user` and `managed`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"rbac_group_ids": schema.SetAttribute{
				ElementType:         types.StringType,
				Optional:            true,
				Computed:            true,
				Default:             setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
				MarkdownDescription: "IDs of the groups (`rbac_group_...`) the person joins on accepting. Only a Claude Enterprise organization with groups accepts a non-empty set. Defaults to an empty set.",
				PlanModifiers: []planmodifier.Set{
					setplanmodifier.RequiresReplace(),
				},
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Status of the invite: `pending` or `accepted`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"invited_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the invite was sent.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"expires_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the invite expires.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"accepted_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the invite was accepted. Null while the invite is pending.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *inviteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *inviteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan inviteResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.OrganizationInviteNewParams{
		Email: plan.Email.ValueString(),
		Role:  anthropic.OrganizationInviteNewParamsRole(plan.Role.ValueString()),
	}
	if !plan.RBACGroupIds.IsNull() && !plan.RBACGroupIds.IsUnknown() {
		var groupIds []string
		resp.Diagnostics.Append(plan.RBACGroupIds.ElementsAs(ctx, &groupIds, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if len(groupIds) > 0 {
			params.RBACGroupIDs = groupIds
		}
	}

	invite, err := r.client.Organization.Invites.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to create invite: %s", err))
		return
	}

	resp.Diagnostics.Append(applyInvite(ctx, &plan, invite)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *inviteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state inviteResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	invite, err := r.client.Organization.Invites.Get(ctx, state.Id.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read invite: %s", err))
		return
	}
	if invite.Status == anthropic.OrganizationInviteStatusDeleted || invite.Status == anthropic.OrganizationInviteStatusExpired {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(applyInvite(ctx, &state, invite)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *inviteResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var plan, state inviteResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if state.Status.ValueString() != string(anthropic.OrganizationInviteStatusAccepted) {
		return
	}
	replaced := !strings.EqualFold(plan.Email.ValueString(), state.Email.ValueString()) ||
		!plan.Role.Equal(state.Role) ||
		!plan.RBACGroupIds.Equal(state.RBACGroupIds)
	if !replaced {
		return
	}
	resp.Diagnostics.AddError("Accepted Invite Cannot Be Replaced",
		fmt.Sprintf("Invite %s was accepted, so %s is already a member and the API rejects a new invite. Manage the member with anthropic_user and remove this invite from the configuration. Destroying an accepted invite leaves the member in place.", state.Id.ValueString(), state.Email.ValueString()))
}

// Update runs only for a case-only email change, because every other argument
// forces a replacement.
func (r *inviteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state inviteResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.Email = plan.Email
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *inviteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state inviteResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.Id.ValueString()
	invite, err := r.client.Organization.Invites.Get(ctx, id)
	if isNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read invite: %s", err))
		return
	}
	// The API answers 400 when asked to withdraw an invite that is not pending.
	if invite.Status != anthropic.OrganizationInviteStatusPending {
		return
	}

	if _, err := r.client.Organization.Invites.Delete(ctx, id); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to withdraw invite: %s", err))
		return
	}
}

func (r *inviteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func emailChangedIgnoringCase(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
	resp.RequiresReplace = !strings.EqualFold(req.StateValue.ValueString(), req.PlanValue.ValueString())
}

// applyInvite keeps an email that differs from the API's only in case, so a
// mixed-case address in configuration does not plan a replacement.
func applyInvite(ctx context.Context, model *inviteResourceModel, invite *anthropic.OrganizationInvite) diag.Diagnostics {
	model.Id = types.StringValue(invite.ID)
	if !strings.EqualFold(model.Email.ValueString(), invite.Email) {
		model.Email = types.StringValue(invite.Email)
	}
	model.Role = types.StringValue(string(invite.Role))
	model.Status = types.StringValue(string(invite.Status))
	model.InvitedAt = timestampValue(invite.InvitedAt)
	model.ExpiresAt = timestampValue(invite.ExpiresAt)
	model.AcceptedAt = timestampValue(invite.AcceptedAt)

	groupIds := invite.RBACGroupIDs
	if groupIds == nil {
		groupIds = []string{}
	}
	groups, diags := types.SetValueFrom(ctx, types.StringType, groupIds)
	model.RBACGroupIds = groups
	return diags
}
