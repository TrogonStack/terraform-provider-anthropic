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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &workspaceServiceAccountResource{}
	_ resource.ResourceWithImportState = &workspaceServiceAccountResource{}
)

func newWorkspaceServiceAccount() resource.Resource { return &workspaceServiceAccountResource{} }

type workspaceServiceAccountResource struct {
	client *anthropic.Client
}

type workspaceServiceAccountResourceModel struct {
	Id               types.String `tfsdk:"id"`
	WorkspaceId      types.String `tfsdk:"workspace_id"`
	ServiceAccountId types.String `tfsdk:"service_account_id"`
	WorkspaceRole    types.String `tfsdk:"workspace_role"`
	Implicit         types.Bool   `tfsdk:"implicit"`
	CreatedByActorId types.String `tfsdk:"created_by_actor_id"`
}

func (r *workspaceServiceAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace_service_account"
}

func (r *workspaceServiceAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a service account's membership in an Anthropic workspace through the Admin API.

A service account must be an explicit member of a workspace before a
federation rule targeting it can issue a token scoped to that workspace.
Every service account already holds an implicit ` + "`workspace_user`" + ` membership
in the organization's default workspace; this resource manages an explicit
membership with a chosen role instead.

This endpoint accepts only an OAuth access token with the ` + "`org:admin`" + ` scope,
through the provider's ` + "`auth_token`" + ` attribute, ` + "`ANTHROPIC_AUTH_TOKEN`" + `, or
Workload Identity Federation. An Admin API key is rejected.

Destroying this resource removes the membership. The Admin API does not
document the effect of removing a service account's only membership in its
default workspace, so a destroy there surfaces whatever the API returns. A
membership removed outside Terraform is removed from state and created again
on the next apply.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<workspace_id>/<service_account_id>`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"workspace_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Tagged ID of the workspace.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"service_account_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Tagged ID of the service account.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"workspace_role": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Role granted to the service account in this workspace: `workspace_admin`, `workspace_developer`, `workspace_restricted_developer`, or `workspace_user`. A service account cannot hold `workspace_billing`.",
				Validators: []validator.String{
					stringvalidator.OneOf("workspace_admin", "workspace_developer", "workspace_restricted_developer", "workspace_user"),
				},
			},
			"implicit": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "True when this is the implicit default-workspace membership every service account has absent an explicit one. An implicit membership always has role `workspace_user` and cannot be removed.",
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"created_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID of the actor that created this membership.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *workspaceServiceAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *workspaceServiceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan workspaceServiceAccountResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.OrganizationWorkspaceServiceAccountAddParams{
		ServiceAccountID: plan.ServiceAccountId.ValueString(),
		WorkspaceRole:    anthropic.NoBillingWorkspaceRole(plan.WorkspaceRole.ValueString()),
	}

	member, err := r.client.Organization.Workspaces.ServiceAccounts.Add(ctx, plan.WorkspaceId.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to add service account to workspace: %s", err))
		return
	}

	applyWorkspaceServiceAccount(&plan, member)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *workspaceServiceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state workspaceServiceAccountResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	member, err := r.client.Organization.Workspaces.ServiceAccounts.Get(ctx, state.ServiceAccountId.ValueString(), anthropic.OrganizationWorkspaceServiceAccountGetParams{
		WorkspaceID: state.WorkspaceId.ValueString(),
	})
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read workspace service account membership: %s", err))
		return
	}

	applyWorkspaceServiceAccount(&state, member)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *workspaceServiceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state workspaceServiceAccountResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.OrganizationWorkspaceServiceAccountUpdateParams{
		WorkspaceID:   state.WorkspaceId.ValueString(),
		WorkspaceRole: anthropic.NoBillingWorkspaceRole(plan.WorkspaceRole.ValueString()),
	}

	member, err := r.client.Organization.Workspaces.ServiceAccounts.Update(ctx, state.ServiceAccountId.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to update workspace service account membership: %s", err))
		return
	}

	applyWorkspaceServiceAccount(&plan, member)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *workspaceServiceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state workspaceServiceAccountResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.Organization.Workspaces.ServiceAccounts.Remove(ctx, state.ServiceAccountId.ValueString(), anthropic.OrganizationWorkspaceServiceAccountRemoveParams{
		WorkspaceID: state.WorkspaceId.ValueString(),
	})
	if err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to remove service account from workspace: %s", err))
		return
	}
}

func (r *workspaceServiceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	workspaceId, serviceAccountId, ok := strings.Cut(req.ID, "/")
	if !ok || workspaceId == "" || serviceAccountId == "" {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected an import ID in the form <workspace_id>/<service_account_id>, got: %s", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workspace_id"), workspaceId)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service_account_id"), serviceAccountId)...)
}

func applyWorkspaceServiceAccount(model *workspaceServiceAccountResourceModel, member *anthropic.ServiceAccountWorkspaceMember) {
	model.Id = types.StringValue(member.WorkspaceID + "/" + member.ServiceAccountID)
	model.WorkspaceId = types.StringValue(member.WorkspaceID)
	model.ServiceAccountId = types.StringValue(member.ServiceAccountID)
	model.WorkspaceRole = types.StringValue(string(member.WorkspaceRole))
	model.Implicit = types.BoolValue(member.Implicit)
	model.CreatedByActorId = types.StringValue(member.CreatedByActorID)
}
