package provider

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const apiKeyImportOnlyMessage = "The Admin API cannot create API keys. Create the key in the Claude Console, then import it: terraform import anthropic_api_key.<name> apikey_..."

var (
	_ resource.Resource                = &apiKeyResource{}
	_ resource.ResourceWithImportState = &apiKeyResource{}
	_ resource.ResourceWithModifyPlan  = &apiKeyResource{}
)

func newAPIKey() resource.Resource { return &apiKeyResource{} }

type apiKeyResource struct {
	client *anthropic.Client
}

// apiKeyModel is shared by anthropic_api_key and the api_keys element of
// anthropic_api_keys, since both expose the same attributes for one key.
type apiKeyModel struct {
	Id             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Status         types.String `tfsdk:"status"`
	CreatedAt      types.String `tfsdk:"created_at"`
	ExpiresAt      types.String `tfsdk:"expires_at"`
	PartialKeyHint types.String `tfsdk:"partial_key_hint"`
	CreatedBy      types.Object `tfsdk:"created_by"`
	Principal      types.Object `tfsdk:"principal"`
	Scope          types.Object `tfsdk:"scope"`
}

var apiKeyCreatedByAttrTypes = map[string]attr.Type{
	"id":   types.StringType,
	"type": types.StringType,
}

var apiKeyPrincipalAttrTypes = map[string]attr.Type{
	"type":               types.StringType,
	"user_id":            types.StringType,
	"service_account_id": types.StringType,
}

var apiKeyScopeAttrTypes = map[string]attr.Type{
	"type":         types.StringType,
	"workspace_id": types.StringType,
}

// apiKeyFromAPI maps an API key returned by the Admin API into the model
// shared by the resource and the data source.
func apiKeyFromAPI(key anthropic.APIKey) (apiKeyModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	model := apiKeyModel{
		Id:             types.StringValue(key.ID),
		Name:           types.StringValue(key.Name),
		Status:         types.StringValue(string(key.Status)),
		CreatedAt:      timestampValue(key.CreatedAt),
		ExpiresAt:      timestampValue(key.ExpiresAt),
		PartialKeyHint: types.StringValue(key.PartialKeyHint),
	}

	createdBy := types.ObjectNull(apiKeyCreatedByAttrTypes)
	if key.CreatedBy.JSON.ID.Valid() {
		var d diag.Diagnostics
		createdBy, d = types.ObjectValue(apiKeyCreatedByAttrTypes, map[string]attr.Value{
			"id":   types.StringValue(key.CreatedBy.ID),
			"type": types.StringValue(string(key.CreatedBy.Type)),
		})
		diags.Append(d...)
	}
	model.CreatedBy = createdBy

	principal := types.ObjectNull(apiKeyPrincipalAttrTypes)
	if key.Principal.JSON.Type.Valid() {
		userId := types.StringNull()
		serviceAccountId := types.StringNull()
		switch key.Principal.Type {
		case "user_actor":
			userId = types.StringValue(key.Principal.UserID)
		case "service_account_actor":
			serviceAccountId = types.StringValue(key.Principal.ServiceAccountID)
		}
		var d diag.Diagnostics
		principal, d = types.ObjectValue(apiKeyPrincipalAttrTypes, map[string]attr.Value{
			"type":               types.StringValue(key.Principal.Type),
			"user_id":            userId,
			"service_account_id": serviceAccountId,
		})
		diags.Append(d...)
	}
	model.Principal = principal

	workspaceId := types.StringNull()
	if key.Scope.Type == "workspace" {
		workspaceId = types.StringValue(key.Scope.WorkspaceID)
	}
	scope, d := types.ObjectValue(apiKeyScopeAttrTypes, map[string]attr.Value{
		"type":         types.StringValue(key.Scope.Type),
		"workspace_id": workspaceId,
	})
	diags.Append(d...)
	model.Scope = scope

	return model, diags
}

func (r *apiKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_key"
}

func (r *apiKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Adopts an Anthropic API key through the Admin API.

The Admin API cannot create API keys and never returns an existing key's
secret value, so this resource is import-only: create the key in the Claude
Console, then run ` + "`terraform import anthropic_api_key.<name> apikey_...`" + `
to bring it under management. A plain ` + "`resource \"anthropic_api_key\"`" + `
block applied without an import fails, because there is nothing to create.

Destroying this resource only stops managing the key and leaves it unchanged.
An archived key cannot be recovered, so Delete never calls the API; set
` + "`status`" + ` to ` + "`inactive`" + ` or ` + "`archived`" + ` from a reviewed change instead
of relying on destroy to deactivate or archive a key.`,
		Attributes: map[string]schema.Attribute{
			"id": rsId(),
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The API key name. Must not be empty.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"status": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Status of the API key: `active`, `inactive` or `archived`. `expired` is a read-only status the API assigns and this resource cannot set. Leave unset to keep whatever status the key already has.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					stringvalidator.OneOf("active", "inactive", "archived"),
				},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the API key was created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"expires_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the API key expires. Null when the key never expires.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"partial_key_hint": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Partially redacted hint for the API key, e.g. `sk-ant-api03-R2D...igAA`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"created_by": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The actor that created the API key. Null for a legacy, workload-identity-federated, or system-created key.",
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.UseStateForUnknown(),
				},
				Attributes: map[string]schema.Attribute{
					"id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "ID of the actor that created the API key.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.UseStateForUnknown(),
						},
					},
					"type": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Type of the actor that created the API key: `user` or `service_account`.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.UseStateForUnknown(),
						},
					},
				},
			},
			"principal": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The principal the API key acts as. Null when the key is not bound to a principal.",
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.UseStateForUnknown(),
				},
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Principal type: `user_actor` or `service_account_actor`.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.UseStateForUnknown(),
						},
					},
					"user_id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "ID of the User the API key acts as. Null unless `type` is `user_actor`.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.UseStateForUnknown(),
						},
					},
					"service_account_id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "ID of the Service Account the API key acts as. Null unless `type` is `service_account_actor`.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.UseStateForUnknown(),
						},
					},
				},
			},
			"scope": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Where the API key belongs: its workspace, or the organization for a principal-bound key with no workspace.",
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.UseStateForUnknown(),
				},
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Scope type: `organization` or `workspace`.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.UseStateForUnknown(),
						},
					},
					"workspace_id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "ID of the workspace the API key belongs to. Null for the `organization` scope.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.UseStateForUnknown(),
						},
					},
				},
			},
		},
	}
}

func (r *apiKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ModifyPlan fails a create attempt at plan time, before Create ever runs,
// so that a plain resource block with no import surfaces the error on
// `terraform plan` rather than only on apply.
func (r *apiKeyResource) ModifyPlan(_ context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() && !req.Plan.Raw.IsNull() {
		resp.Diagnostics.AddError("API Keys Cannot Be Created", apiKeyImportOnlyMessage)
	}
}

func (r *apiKeyResource) Create(_ context.Context, _ resource.CreateRequest, resp *resource.CreateResponse) {
	resp.Diagnostics.AddError("API Keys Cannot Be Created", apiKeyImportOnlyMessage)
}

func (r *apiKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state apiKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	key, err := r.client.Organization.APIKeys.Get(ctx, state.Id.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read API key: %s", err))
		return
	}

	model, diags := apiKeyFromAPI(*key)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *apiKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan apiKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.OrganizationAPIKeyUpdateParams{
		Name: anthropic.String(plan.Name.ValueString()),
	}
	if isConfigured(plan.Status) {
		params.Status = anthropic.OrganizationAPIKeyUpdateParamsStatus(plan.Status.ValueString())
	}

	key, err := r.client.Organization.APIKeys.Update(ctx, plan.Id.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to update API key: %s", err))
		return
	}

	model, diags := apiKeyFromAPI(*key)
	resp.Diagnostics.Append(diags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

// Delete adopts a key this resource did not create, so it only forgets the
// key: it never archives or deactivates it. An archived key cannot be
// recovered, and removing a block from config must not silently kill
// production credentials.
func (r *apiKeyResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

func (r *apiKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
