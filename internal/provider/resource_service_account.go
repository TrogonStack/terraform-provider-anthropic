package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var serviceAccountNamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

var (
	_ resource.Resource                = &serviceAccountResource{}
	_ resource.ResourceWithImportState = &serviceAccountResource{}
)

func newServiceAccount() resource.Resource { return &serviceAccountResource{} }

type serviceAccountResource struct {
	client *anthropic.Client
}

type serviceAccountResourceModel struct {
	Id                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Description       types.String `tfsdk:"description"`
	OrganizationRole  types.String `tfsdk:"organization_role"`
	CreatedAt         types.String `tfsdk:"created_at"`
	UpdatedAt         types.String `tfsdk:"updated_at"`
	CreatedByActorId  types.String `tfsdk:"created_by_actor_id"`
	UpdatedByActorId  types.String `tfsdk:"updated_by_actor_id"`
}

func (r *serviceAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account"
}

func (r *serviceAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages an Anthropic service account through the Admin API.

A service account is a named non-human identity that a workload identity
federation rule can target, letting a CI pipeline or another automated caller
exchange a federation token for a scoped Anthropic credential without a
long-lived secret.

This endpoint accepts only an OAuth access token with the ` + "`org:admin`" + ` scope,
through the provider's ` + "`auth_token`" + ` attribute, ` + "`ANTHROPIC_AUTH_TOKEN`" + `, or
Workload Identity Federation. An Admin API key is rejected.

` + "`name`" + ` is a slug the Admin API cannot rename, so changing it replaces the
service account: the old one is archived and a new one is created.

Destroying this resource archives the service account. Archiving cannot be
undone and is rejected with an API error while a live federation rule still
targets the service account; archive or retarget that rule first. A service
account archived or deleted outside Terraform is removed from state and
created again on the next apply.`,
		Attributes: map[string]schema.Attribute{
			"id": rsId(),
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Slug identifier, matching `^[a-z0-9-]+$`, 1 to 255 characters. Unique within the organization; a duplicate name is rejected. The API has no rename operation, so changing this replaces the service account, which archives the old one.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 255),
					stringvalidator.RegexMatches(serviceAccountNamePattern, "must contain only lowercase letters, digits, and hyphens"),
				},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-text description. The API stores an unset description as an empty string, which this provider treats as null, so leave the attribute out instead of setting it to `\"\"`.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"organization_role": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Org-level role, `admin` or `developer`. The API defaults to `developer` when unset on create. A workload identity federation rule may be created or retargeted to grant `org:admin` scope only when this is `admin`; rules that grant `org:admin` are managed in the Claude Console, not through this provider.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf("admin", "developer"),
				},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the service account was created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the service account was last updated.",
			},
			"created_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID of the actor that created the service account.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID of the actor that last updated the service account.",
			},
		},
	}
}

func (r *serviceAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *serviceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceAccountResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.OrganizationServiceAccountNewParams{
		Name: plan.Name.ValueString(),
	}
	if isConfigured(plan.Description) {
		params.Description = anthropic.String(plan.Description.ValueString())
	}
	if isConfigured(plan.OrganizationRole) {
		params.OrganizationRole = anthropic.OrganizationServiceAccountNewParamsOrganizationRole(plan.OrganizationRole.ValueString())
	}

	serviceAccount, err := r.client.Organization.ServiceAccounts.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to create service account: %s", err))
		return
	}

	applyServiceAccount(&plan, serviceAccount)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serviceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceAccountResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serviceAccount, err := r.client.Organization.ServiceAccounts.Get(ctx, state.Id.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read service account: %s", err))
		return
	}
	if !serviceAccount.ArchivedAt.IsZero() {
		resp.State.RemoveResource(ctx)
		return
	}

	applyServiceAccount(&state, serviceAccount)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serviceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serviceAccountResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var params anthropic.OrganizationServiceAccountUpdateParams
	if !plan.Description.Equal(state.Description) {
		if isConfigured(plan.Description) {
			params.Description = anthropic.String(plan.Description.ValueString())
		} else {
			params.Description = param.Null[string]()
		}
	}
	if isConfigured(plan.OrganizationRole) && !plan.OrganizationRole.Equal(state.OrganizationRole) {
		params.OrganizationRole = anthropic.OrganizationServiceAccountUpdateParamsOrganizationRole(plan.OrganizationRole.ValueString())
	}

	serviceAccount, err := r.client.Organization.ServiceAccounts.Update(ctx, state.Id.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to update service account: %s", err))
		return
	}

	applyServiceAccount(&plan, serviceAccount)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serviceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serviceAccountResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.Id.ValueString()
	serviceAccount, err := r.client.Organization.ServiceAccounts.Get(ctx, id)
	if isNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read service account: %s", err))
		return
	}
	if !serviceAccount.ArchivedAt.IsZero() {
		return
	}

	if _, err := r.client.Organization.ServiceAccounts.Archive(ctx, id); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to archive service account: %s", err))
		return
	}
}

func (r *serviceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func applyServiceAccount(model *serviceAccountResourceModel, serviceAccount *anthropic.ServiceAccount) {
	model.Id = types.StringValue(serviceAccount.ID)
	model.Name = types.StringValue(serviceAccount.Name)
	model.Description = optionalString(serviceAccount.Description)
	model.OrganizationRole = types.StringValue(string(serviceAccount.OrganizationRole))
	model.CreatedAt = timestampValue(serviceAccount.CreatedAt)
	model.UpdatedAt = timestampValue(serviceAccount.UpdatedAt)
	model.CreatedByActorId = types.StringValue(serviceAccount.CreatedByActorID)
	model.UpdatedByActorId = types.StringValue(serviceAccount.UpdatedByActorID)
}
