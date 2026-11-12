package provider

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
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

const rbacGroupSCIMSummary = "RBAC Group Managed by SCIM"

var (
	_ resource.Resource                = &rbacGroupResource{}
	_ resource.ResourceWithImportState = &rbacGroupResource{}
	_ resource.ResourceWithModifyPlan  = &rbacGroupResource{}
)

func newRBACGroup() resource.Resource { return &rbacGroupResource{} }

type rbacGroupResource struct {
	client *anthropic.Client
}

type rbacGroupResourceModel struct {
	Id         types.String `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	SourceType types.String `tfsdk:"source_type"`
	RoleIds    types.Set    `tfsdk:"role_ids"`
	CreatedAt  types.String `tfsdk:"created_at"`
	UpdatedAt  types.String `tfsdk:"updated_at"`
}

func (r *rbacGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_rbac_group"
}

func (r *rbacGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages an RBAC group in a Claude Enterprise organization through the Admin API.

An RBAC group connects members to the custom roles attached to it. Groups
belong to the parent organization and every organization under it. Attaching
roles to a group happens in claude.ai organization settings; the API reads
` + "`role_ids`" + ` but cannot change them.

The RBAC Groups API is available to Claude Enterprise organizations only. It
needs a key created in claude.ai > Organization settings > API for all linked
organizations, with the ` + "`read:rbac_groups`" + ` and ` + "`write:rbac_groups`" + ` scopes.

A group provisioned by an identity provider (` + "`source_type`" + ` ` + "`scim`" + `) can be
imported and read, but the API rejects a rename or delete of it. Terraform
fails the plan instead: rename it in the identity provider, and stop managing
it with a ` + "`removed`" + ` block whose ` + "`lifecycle`" + ` sets ` + "`destroy = false`" + `.

Destroying this resource deletes the group. Its members stay in their
organizations but lose the permissions of its attached roles, and a group
spend limit stops applying to them. A group deleted outside Terraform is
removed from state and created again on the next apply.`,
		Attributes: map[string]schema.Attribute{
			"id": rsId(),
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the RBAC group. The API does not enforce uniqueness. A change renames the group in place.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"source_type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "How the group was created: `direct` for a group created through the API or claude.ai, `scim` for a group provisioned by the identity provider.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"role_ids": schema.SetAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "IDs of the custom roles attached to the group in claude.ai organization settings. When the API reports role data as temporarily unavailable, the last known value is kept.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the group was created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the group was last updated.",
			},
		},
	}
}

func (r *rbacGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ModifyPlan fails a rename or destroy of a SCIM group at plan time, since
// the API rejects both with a 400 while the identity provider owns the group.
func (r *rbacGroupResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() {
		return
	}
	var state rbacGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || state.SourceType.ValueString() != string(anthropic.BetaRBACGroupSourceTypeSCIM) {
		return
	}

	if req.Plan.Raw.IsNull() {
		resp.Diagnostics.AddError(rbacGroupSCIMSummary, rbacGroupSCIMDeleteMessage(state.Id.ValueString()))
		return
	}

	var plan rbacGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.Name.IsUnknown() && !plan.Name.Equal(state.Name) {
		resp.Diagnostics.AddAttributeError(path.Root("name"), rbacGroupSCIMSummary,
			fmt.Sprintf("RBAC group %s is provisioned by an identity provider (source_type \"scim\"), and the API rejects a rename of it. Rename it in the identity provider, or set name back to %q.", state.Id.ValueString(), state.Name.ValueString()))
	}
}

func (r *rbacGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan rbacGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, err := r.client.Beta.Organization.RBACGroups.New(ctx, anthropic.BetaOrganizationRBACGroupNewParams{
		Name: plan.Name.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to create RBAC group: %s", err))
		return
	}

	resp.Diagnostics.Append(applyRBACGroup(ctx, &plan, group, types.SetNull(types.StringType))...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *rbacGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state rbacGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, err := r.client.Beta.Organization.RBACGroups.Get(ctx, state.Id.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read RBAC group: %s", err))
		return
	}

	resp.Diagnostics.Append(applyRBACGroup(ctx, &state, group, state.RoleIds)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *rbacGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state rbacGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.Id.ValueString()
	group, err := r.client.Beta.Organization.RBACGroups.Update(ctx, id, anthropic.BetaOrganizationRBACGroupUpdateParams{
		Name: anthropic.String(plan.Name.ValueString()),
	})
	if err != nil {
		if scim, _ := rbacGroupIsSCIM(ctx, r.client, id); scim {
			resp.Diagnostics.AddError(rbacGroupSCIMSummary, fmt.Sprintf("RBAC group %s is provisioned by an identity provider (source_type \"scim\"), so the API rejected the rename: %s", id, err))
			return
		}
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to update RBAC group: %s", err))
		return
	}

	resp.Diagnostics.Append(applyRBACGroup(ctx, &plan, group, state.RoleIds)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *rbacGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state rbacGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.Id.ValueString()
	_, err := r.client.Beta.Organization.RBACGroups.Delete(ctx, id)
	if err == nil || isNotFound(err) {
		return
	}
	if scim, _ := rbacGroupIsSCIM(ctx, r.client, id); scim {
		resp.Diagnostics.AddError(rbacGroupSCIMSummary, rbacGroupSCIMDeleteMessage(id))
		return
	}
	resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to delete RBAC group: %s", err))
}

func (r *rbacGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func rbacGroupSCIMDeleteMessage(id string) string {
	return fmt.Sprintf("RBAC group %s is provisioned by an identity provider (source_type \"scim\"), and the API rejects a delete of it. Delete it in the identity provider, or stop managing it without deleting it: replace the resource block with a removed block whose lifecycle sets destroy = false, or run terraform state rm.", id)
}

func rbacGroupIsSCIM(ctx context.Context, client *anthropic.Client, id string) (bool, error) {
	group, err := client.Beta.Organization.RBACGroups.Get(ctx, id)
	if err != nil {
		return false, err
	}
	return group.SourceType == anthropic.BetaRBACGroupSourceTypeSCIM, nil
}

// applyRBACGroup keeps priorRoleIds when the API returns role_ids as null,
// which it documents as role data being temporarily unavailable.
func applyRBACGroup(ctx context.Context, model *rbacGroupResourceModel, group *anthropic.BetaRBACGroup, priorRoleIds types.Set) diag.Diagnostics {
	model.Id = types.StringValue(group.ID)
	model.Name = types.StringValue(group.Name)
	model.SourceType = types.StringValue(string(group.SourceType))
	model.CreatedAt = timestampValue(group.CreatedAt)
	model.UpdatedAt = timestampValue(group.UpdatedAt)

	if !group.JSON.RoleIDs.Valid() {
		if priorRoleIds.IsUnknown() {
			priorRoleIds = types.SetNull(types.StringType)
		}
		model.RoleIds = priorRoleIds
		return nil
	}
	roleIds, diags := sortedStringSet(ctx, group.RoleIDs)
	model.RoleIds = roleIds
	return diags
}
