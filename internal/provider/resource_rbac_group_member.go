package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &rbacGroupMemberResource{}
	_ resource.ResourceWithImportState = &rbacGroupMemberResource{}
	_ resource.ResourceWithModifyPlan  = &rbacGroupMemberResource{}
)

func newRBACGroupMember() resource.Resource { return &rbacGroupMemberResource{} }

type rbacGroupMemberResource struct {
	client *anthropic.Client
}

type rbacGroupMemberResourceModel struct {
	Id          types.String `tfsdk:"id"`
	RBACGroupId types.String `tfsdk:"rbac_group_id"`
	UserId      types.String `tfsdk:"user_id"`
	Email       types.String `tfsdk:"email"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

func (r *rbacGroupMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_rbac_group_member"
}

func (r *rbacGroupMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages one user's membership in an RBAC group of a Claude Enterprise organization through the Admin API.

The user must already be a member of one of the enterprise's organizations.
Adding a user who is already in the group is rejected by the API; import that
membership instead.

The RBAC Groups API is available to Claude Enterprise organizations only. It
needs a key created in claude.ai > Organization settings > API for all linked
organizations, with the ` + "`read:rbac_groups`" + ` and ` + "`write:rbac_groups`" + ` scopes.

The membership of a group provisioned by an identity provider (` + "`source_type`" + `
` + "`scim`" + `) is managed in the identity provider. Terraform fails the plan when it
would add or remove a member of such a group; stop managing an imported one
with a ` + "`removed`" + ` block whose ` + "`lifecycle`" + ` sets ` + "`destroy = false`" + `.

Destroying this resource removes the user from the group; the user stays a
member of their organization. A membership removed outside Terraform, a user
who left the enterprise, or a group deleted outside Terraform is removed from
state and created again on the next apply.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<rbac_group_id>/<user_id>`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"rbac_group_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the RBAC group.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"user_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the user. A user has the same ID in every organization of the enterprise.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"email": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Email of the user.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the user was added to the group.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *rbacGroupMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ModifyPlan fails an add or remove on a SCIM group at plan time, since the
// API rejects both with a 400 while the identity provider owns the group. A
// failed lookup leaves the check to the API.
func (r *rbacGroupMemberResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if r.client == nil {
		return
	}

	var plan, state rbacGroupMemberResourceModel
	if !req.Plan.Raw.IsNull() {
		resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	}
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if !req.Plan.Raw.IsNull() && !req.State.Raw.IsNull() && plan.RBACGroupId.Equal(state.RBACGroupId) && plan.UserId.Equal(state.UserId) {
		return
	}

	checked := map[string]bool{}
	for _, rbacGroupId := range []types.String{state.RBACGroupId, plan.RBACGroupId} {
		id := rbacGroupId.ValueString()
		if !isConfigured(rbacGroupId) || checked[id] {
			continue
		}
		checked[id] = true
		if scim, err := rbacGroupIsSCIM(ctx, r.client, id); err == nil && scim {
			resp.Diagnostics.AddError(rbacGroupSCIMSummary, rbacGroupMemberSCIMMessage(id))
		}
	}
}

func (r *rbacGroupMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan rbacGroupMemberResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rbacGroupId := plan.RBACGroupId.ValueString()
	member, err := r.client.Beta.Organization.RBACGroups.Members.Add(ctx, rbacGroupId, anthropic.BetaOrganizationRBACGroupMemberAddParams{
		UserID: plan.UserId.ValueString(),
	})
	if err != nil {
		if scim, _ := rbacGroupIsSCIM(ctx, r.client, rbacGroupId); scim {
			resp.Diagnostics.AddError(rbacGroupSCIMSummary, rbacGroupMemberSCIMMessage(rbacGroupId))
			return
		}
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to add user to RBAC group: %s", err))
		return
	}

	applyRBACGroupMember(&plan, *member)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *rbacGroupMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state rbacGroupMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	member, found, err := r.findMember(ctx, state.RBACGroupId.ValueString(), state.UserId.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read RBAC group membership: %s", err))
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}

	applyRBACGroupMember(&state, member)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *rbacGroupMemberResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Unexpected Update", "Every attribute of anthropic_rbac_group_member requires replacement; please report this as a provider bug.")
}

func (r *rbacGroupMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state rbacGroupMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rbacGroupId := state.RBACGroupId.ValueString()
	_, err := r.client.Beta.Organization.RBACGroups.Members.Remove(ctx, state.UserId.ValueString(), anthropic.BetaOrganizationRBACGroupMemberRemoveParams{
		RBACGroupID: rbacGroupId,
	})
	if err == nil || isNotFound(err) {
		return
	}
	if scim, _ := rbacGroupIsSCIM(ctx, r.client, rbacGroupId); scim {
		resp.Diagnostics.AddError(rbacGroupSCIMSummary, rbacGroupMemberSCIMMessage(rbacGroupId))
		return
	}
	resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to remove user from RBAC group: %s", err))
}

// findMember pages through the group's members, since the API has no
// endpoint that reads a single membership.
func (r *rbacGroupMemberResource) findMember(ctx context.Context, rbacGroupId, userId string) (anthropic.BetaRBACGroupMember, bool, error) {
	pager := r.client.Beta.Organization.RBACGroups.Members.ListAutoPaging(ctx, rbacGroupId, anthropic.BetaOrganizationRBACGroupMemberListParams{
		Limit: anthropic.Int(1000),
	})
	for pager.Next() {
		if member := pager.Current(); member.UserID == userId {
			return member, true, nil
		}
	}
	return anthropic.BetaRBACGroupMember{}, false, pager.Err()
}

func (r *rbacGroupMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	rbacGroupId, userId, ok := strings.Cut(req.ID, "/")
	if !ok || rbacGroupId == "" || userId == "" {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected an import ID in the form <rbac_group_id>/<user_id>, got: %s", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("rbac_group_id"), rbacGroupId)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), userId)...)
}

func rbacGroupMemberSCIMMessage(rbacGroupId string) string {
	return fmt.Sprintf("RBAC group %s is provisioned by an identity provider (source_type \"scim\"), so its membership is managed there and the API rejects adding or removing a member. Change the membership in the identity provider. To stop managing an imported membership without removing it, replace the resource block with a removed block whose lifecycle sets destroy = false, or run terraform state rm.", rbacGroupId)
}

func applyRBACGroupMember(model *rbacGroupMemberResourceModel, member anthropic.BetaRBACGroupMember) {
	model.Id = types.StringValue(member.RBACGroupID + "/" + member.UserID)
	model.RBACGroupId = types.StringValue(member.RBACGroupID)
	model.UserId = types.StringValue(member.UserID)
	model.Email = types.StringValue(member.Email)
	model.CreatedAt = timestampValue(member.CreatedAt)
}
