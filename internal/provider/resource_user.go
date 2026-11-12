package provider

import (
	"context"
	"fmt"

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

const userImportOnlyMessage = "The Admin API cannot add members to an organization directly. Invite the person with anthropic_invite, or add them in the organization settings, then import the member: terraform import anthropic_user.<name> user_..."

var (
	_ resource.Resource                = &userResource{}
	_ resource.ResourceWithImportState = &userResource{}
	_ resource.ResourceWithModifyPlan  = &userResource{}
)

func newUser() resource.Resource { return &userResource{} }

type userResource struct {
	client *anthropic.Client
}

// userModel is shared by anthropic_user and the users element of
// anthropic_users, since both expose the same attributes for one member.
type userModel struct {
	Id      types.String `tfsdk:"id"`
	Email   types.String `tfsdk:"email"`
	Name    types.String `tfsdk:"name"`
	Role    types.String `tfsdk:"role"`
	AddedAt types.String `tfsdk:"added_at"`
}

func userFromAPI(user anthropic.OrganizationUser) userModel {
	return userModel{
		Id:      types.StringValue(user.ID),
		Email:   types.StringValue(user.Email),
		Name:    types.StringValue(user.Name),
		Role:    types.StringValue(string(user.Role)),
		AddedAt: timestampValue(user.AddedAt),
	}
}

func (r *userResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (r *userResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Adopts a member of the organization through the Admin API and manages
the member's organization role.

The Admin API cannot add a member directly, so this resource is import-only: invite the
person with ` + "`anthropic_invite`" + ` or add them in the organization settings, then run
` + "`terraform import anthropic_user.<name> user_...`" + ` to bring the member under
management. A plain ` + "`resource \"anthropic_user\"`" + ` block applied without an import
fails, because there is nothing to create.

Destroying this resource only stops managing the member and leaves them in the
organization with their current role. Removing a member cannot be undone and
this resource did not add them, so Delete never calls the API.

Needs ` + "`read:members`" + ` and ` + "`write:members`" + ` on a Claude Enterprise key, plus
` + "`write:rbac_groups`" + ` to set the ` + "`managed`" + ` role.`,
		Attributes: map[string]schema.Attribute{
			"id": rsId(),
			"role": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Organization role of the member. The API decides which roles it can assign. A Claude Console organization accepts `user`, `developer`, `billing` and `claude_code_user`; `admin` cannot be assigned through the API. A Claude Enterprise organization accepts `user` and `managed`, and rejects a change to a member who holds `owner`, `membership_admin` or `primary_owner`, or whose role the identity provider manages.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"email": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Email of the member.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Name of the member.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"added_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the member joined the organization.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *userResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *userResource) ModifyPlan(_ context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() && !req.Plan.Raw.IsNull() {
		resp.Diagnostics.AddError("Users Cannot Be Created", userImportOnlyMessage)
	}
}

func (r *userResource) Create(_ context.Context, _ resource.CreateRequest, resp *resource.CreateResponse) {
	resp.Diagnostics.AddError("Users Cannot Be Created", userImportOnlyMessage)
}

func (r *userResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	user, err := r.client.Organization.Users.Get(ctx, state.Id.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read user: %s", err))
		return
	}

	model := userFromAPI(*user)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *userResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan userModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	user, err := r.client.Organization.Users.Update(ctx, plan.Id.ValueString(), anthropic.OrganizationUserUpdateParams{
		Role: anthropic.OrganizationUserUpdateParamsRole(plan.Role.ValueString()),
	})
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to update user: %s", err))
		return
	}

	model := userFromAPI(*user)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

// Delete never removes the member: this resource did not add them, and a
// removal cannot be undone.
func (r *userResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

func (r *userResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
