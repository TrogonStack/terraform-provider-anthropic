package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &workspaceMemberResource{}
	_ resource.ResourceWithImportState = &workspaceMemberResource{}
)

var assignableWorkspaceRoles = []string{"workspace_admin", "workspace_developer", "workspace_restricted_developer", "workspace_user"}

func newWorkspaceMember() resource.Resource { return &workspaceMemberResource{} }

type workspaceMemberResource struct {
	client *anthropic.Client
}

type workspaceMemberResourceModel struct {
	Id            types.String `tfsdk:"id"`
	WorkspaceId   types.String `tfsdk:"workspace_id"`
	UserId        types.String `tfsdk:"user_id"`
	WorkspaceRole types.String `tfsdk:"workspace_role"`
}

func (r *workspaceMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace_member"
}

func (r *workspaceMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a user's membership in an Anthropic workspace through the Admin API.

The user must already belong to the organization. Organization users and
developers must be added to each workspace explicitly. Organization admins
and billing members hold ` + "`workspace_admin`" + ` and ` + "`workspace_billing`" + `
in every workspace through their organization role; that membership cannot be
added, changed, or removed here, so change the organization role instead.

Creating this resource refuses to take over a membership that already exists,
including an inherited one; import an explicit membership instead. Destroying
this resource removes the user from the workspace only, never from the
organization. A membership removed outside Terraform, or a workspace archived
outside Terraform, is removed from state and created again on the next apply.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<workspace_id>/<user_id>`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"workspace_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the workspace.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"user_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the user.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"workspace_role": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Role granted to the user in this workspace: `workspace_admin`, `workspace_developer`, `workspace_restricted_developer`, or `workspace_user`. `workspace_billing` cannot be assigned; it comes only from the organization billing role.",
				Validators: []validator.String{
					stringvalidator.OneOf(assignableWorkspaceRoles...),
				},
			},
		},
	}
}

func (r *workspaceMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *workspaceMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan workspaceMemberResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceId := plan.WorkspaceId.ValueString()
	userId := plan.UserId.ValueString()

	existing, err := r.client.Organization.Workspaces.Members.Get(ctx, userId, anthropic.OrganizationWorkspaceMemberGetParams{
		WorkspaceID: workspaceId,
	})
	if err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to check for an existing workspace membership: %s", err))
		return
	}
	if err == nil {
		resp.Diagnostics.AddError("Membership Already Exists",
			fmt.Sprintf("User %s is already a member of workspace %s with role %s. If the user is an organization admin or billing member, that role is inherited and cannot be managed by this resource. Otherwise import it instead: terraform import anthropic_workspace_member.<name> %s/%s", userId, workspaceId, existing.WorkspaceRole, workspaceId, userId))
		return
	}

	member, err := r.client.Organization.Workspaces.Members.Add(ctx, workspaceId, anthropic.OrganizationWorkspaceMemberAddParams{
		UserID:        userId,
		WorkspaceRole: anthropic.NoBillingWorkspaceRole(plan.WorkspaceRole.ValueString()),
	})
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to add user to workspace: %s", err))
		return
	}

	applyWorkspaceMember(&plan, member)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *workspaceMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state workspaceMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	member, err := r.client.Organization.Workspaces.Members.Get(ctx, state.UserId.ValueString(), anthropic.OrganizationWorkspaceMemberGetParams{
		WorkspaceID: state.WorkspaceId.ValueString(),
	})
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		gone, goneErr := r.workspaceGone(ctx, state.WorkspaceId.ValueString())
		if goneErr == nil && gone {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read workspace membership: %s", err))
		return
	}

	applyWorkspaceMember(&state, member)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *workspaceMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state workspaceMemberResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	member, err := r.client.Organization.Workspaces.Members.Update(ctx, state.UserId.ValueString(), anthropic.OrganizationWorkspaceMemberUpdateParams{
		WorkspaceID:   state.WorkspaceId.ValueString(),
		WorkspaceRole: anthropic.WorkspaceRole(plan.WorkspaceRole.ValueString()),
	})
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to update workspace membership: %s", err))
		return
	}

	applyWorkspaceMember(&plan, member)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *workspaceMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state workspaceMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.Organization.Workspaces.Members.Remove(ctx, state.UserId.ValueString(), anthropic.OrganizationWorkspaceMemberRemoveParams{
		WorkspaceID: state.WorkspaceId.ValueString(),
	})
	if err != nil && !isNotFound(err) {
		gone, goneErr := r.workspaceGone(ctx, state.WorkspaceId.ValueString())
		if goneErr == nil && gone {
			return
		}
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to remove user from workspace: %s", err))
		return
	}
}

func (r *workspaceMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	workspaceId, userId, ok := strings.Cut(req.ID, "/")
	if !ok || workspaceId == "" || userId == "" {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected an import ID in the form <workspace_id>/<user_id>, got: %s", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workspace_id"), workspaceId)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), userId)...)
}

func (r *workspaceMemberResource) workspaceGone(ctx context.Context, workspaceID string) (bool, error) {
	workspace, err := r.client.Organization.Workspaces.Get(ctx, workspaceID)
	if isNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return !workspace.ArchivedAt.IsZero(), nil
}

func applyWorkspaceMember(model *workspaceMemberResourceModel, member *anthropic.WorkspaceMember) {
	model.Id = types.StringValue(member.WorkspaceID + "/" + member.UserID)
	model.WorkspaceId = types.StringValue(member.WorkspaceID)
	model.UserId = types.StringValue(member.UserID)
	model.WorkspaceRole = types.StringValue(string(member.WorkspaceRole))
}
