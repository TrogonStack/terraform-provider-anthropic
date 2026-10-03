package provider

import (
	"context"
	"fmt"
	"regexp"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

const (
	defaultWorkspaceGeo        = string(anthropic.DataResidencyCreateConfigWorkspaceGeoUs)
	defaultDefaultInferenceGeo = string(anthropic.DataResidencyCreateConfigDefaultInferenceGeoGlobal)
	reservedTagPrefix          = "anthropic"
)

var (
	hexColorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

var (
	_ resource.Resource                = &workspaceResource{}
	_ resource.ResourceWithImportState = &workspaceResource{}
)

func newWorkspace() resource.Resource { return &workspaceResource{} }

type workspaceResource struct {
	client *anthropic.Client
}

type workspaceResourceModel struct {
	Id            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	DisplayColor  types.String `tfsdk:"display_color"`
	Tags          types.Map    `tfsdk:"tags"`
	ExternalKeyId types.String `tfsdk:"external_key_id"`
	DataResidency types.Object `tfsdk:"data_residency"`
	CompartmentId types.String `tfsdk:"compartment_id"`
	CreatedAt     types.String `tfsdk:"created_at"`
}

var dataResidencyAttrTypes = map[string]attr.Type{
	"workspace_geo":          types.StringType,
	"allowed_inference_geos": types.SetType{ElemType: types.StringType},
	"default_inference_geo":  types.StringType,
}

// dataResidency is the provider's view of a workspace's data residency.
// A nil allowedInferenceGeos means the API's "unrestricted".
type dataResidency struct {
	workspaceGeo         string
	allowedInferenceGeos []anthropic.AllowedInferenceGeo
	defaultInferenceGeo  string
}

func dataResidencyFromAPI(d anthropic.DataResidency) dataResidency {
	residency := dataResidency{
		workspaceGeo:        string(d.WorkspaceGeo),
		defaultInferenceGeo: string(d.DefaultInferenceGeo),
	}
	if d.AllowedInferenceGeos.OfUnrestricted != constant.ValueOf[constant.Unrestricted]() {
		residency.allowedInferenceGeos = append([]anthropic.AllowedInferenceGeo{}, d.AllowedInferenceGeos.OfGeos...)
	}
	return residency
}

func dataResidencyFromObject(ctx context.Context, obj types.Object) (dataResidency, diag.Diagnostics) {
	var model struct {
		WorkspaceGeo         types.String `tfsdk:"workspace_geo"`
		AllowedInferenceGeos types.Set    `tfsdk:"allowed_inference_geos"`
		DefaultInferenceGeo  types.String `tfsdk:"default_inference_geo"`
	}
	diags := obj.As(ctx, &model, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return dataResidency{}, diags
	}
	residency := dataResidency{
		workspaceGeo:        model.WorkspaceGeo.ValueString(),
		defaultInferenceGeo: model.DefaultInferenceGeo.ValueString(),
	}
	if !model.AllowedInferenceGeos.IsNull() {
		residency.allowedInferenceGeos = []anthropic.AllowedInferenceGeo{}
		diags.Append(model.AllowedInferenceGeos.ElementsAs(ctx, &residency.allowedInferenceGeos, false)...)
	}
	return residency, diags
}

func (d dataResidency) isUnrestricted() bool {
	return d.allowedInferenceGeos == nil
}

func (d dataResidency) objectValue(ctx context.Context) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics
	allowed := types.SetNull(types.StringType)
	if !d.isUnrestricted() {
		var setDiags diag.Diagnostics
		allowed, setDiags = types.SetValueFrom(ctx, types.StringType, d.allowedInferenceGeos)
		diags.Append(setDiags...)
	}
	obj, objDiags := types.ObjectValue(dataResidencyAttrTypes, map[string]attr.Value{
		"workspace_geo":          types.StringValue(d.workspaceGeo),
		"allowed_inference_geos": allowed,
		"default_inference_geo":  types.StringValue(d.defaultInferenceGeo),
	})
	diags.Append(objDiags...)
	return obj, diags
}

func (d dataResidency) createParam() anthropic.DataResidencyCreateConfigParam {
	param := anthropic.DataResidencyCreateConfigParam{
		WorkspaceGeo:        anthropic.DataResidencyCreateConfigWorkspaceGeo(d.workspaceGeo),
		DefaultInferenceGeo: anthropic.DataResidencyCreateConfigDefaultInferenceGeo(d.defaultInferenceGeo),
	}
	if d.isUnrestricted() {
		param.AllowedInferenceGeos.OfUnrestricted = constant.ValueOf[constant.Unrestricted]()
	} else {
		param.AllowedInferenceGeos.OfGeos = d.allowedInferenceGeos
	}
	return param
}

func (d dataResidency) updateParam() anthropic.DataResidencyUpdateConfigParam {
	param := anthropic.DataResidencyUpdateConfigParam{
		DefaultInferenceGeo: anthropic.DataResidencyUpdateConfigDefaultInferenceGeo(d.defaultInferenceGeo),
	}
	if d.isUnrestricted() {
		param.AllowedInferenceGeos.OfUnrestricted = constant.ValueOf[constant.Unrestricted]()
	} else {
		param.AllowedInferenceGeos.OfGeos = d.allowedInferenceGeos
	}
	return param
}

func (r *workspaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace"
}

func (r *workspaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	defaultResidency, _ := dataResidency{
		workspaceGeo:        defaultWorkspaceGeo,
		defaultInferenceGeo: defaultDefaultInferenceGeo,
	}.objectValue(context.Background())

	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages an Anthropic workspace through the Admin API.

Destroying this resource archives the workspace. The Admin API has no method to
delete a workspace, and archiving cannot be undone: it archives every API key
created for the workspace. Archived workspaces no longer count toward the
organization's workspace limit.

A workspace archived or deleted outside Terraform is removed from state and
created again on the next apply.`,
		Attributes: map[string]schema.Attribute{
			"id": rsId(),
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The workspace name. Must not be empty.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"display_color": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Hex color code in `#RRGGBB` form representing the workspace in the Claude Console, e.g. `#6C5BB9`. The API assigns one when unset.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(hexColorPattern, "must be a hex color code such as `#6C5BB9`"),
				},
			},
			"tags": schema.MapAttribute{
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				Default:             mapdefault.StaticValue(types.MapValueMust(types.StringType, map[string]attr.Value{})),
				MarkdownDescription: "User-defined tags as string key-value pairs. Keys may not begin with `anthropic`. Terraform manages the whole map, so tags added outside Terraform are removed on the next apply.",
				Validators: []validator.Map{
					mapvalidator.KeysAre(
						stringvalidator.RegexMatches(withoutPrefix(reservedTagPrefix), fmt.Sprintf("must not begin with `%s`", reservedTagPrefix)),
					),
				},
			},
			"external_key_id": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "ID of the customer-managed encryption key (CMEK) configuration for the workspace. Requires CMEK to be enabled for the organization. Write-once: it can be added to a workspace that has none, but once set it cannot be changed or removed, and a plan that tries to is rejected.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					writeOnceString(),
				},
			},
			"data_residency": schema.SingleNestedAttribute{
				Optional:            true,
				Computed:            true,
				Default:             objectdefault.StaticValue(defaultResidency),
				MarkdownDescription: "Data residency configuration. Defaults to the API's own defaults: `workspace_geo = \"us\"`, unrestricted inference geos, and `default_inference_geo = \"global\"`.",
				Attributes: map[string]schema.Attribute{
					"workspace_geo": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						Default:             stringdefault.StaticString(defaultWorkspaceGeo),
						MarkdownDescription: "Geographic region for workspace data storage. Immutable after creation, so changing it replaces the workspace. Defaults to `us`.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
						},
					},
					"allowed_inference_geos": schema.SetAttribute{
						Optional:            true,
						ElementType:         types.StringType,
						MarkdownDescription: "Permitted inference geos, e.g. `[\"us\"]`. Must not be empty; leave unset to allow every geo (the API's `unrestricted`).",
						Validators: []validator.Set{
							setvalidator.SizeAtLeast(1),
						},
					},
					"default_inference_geo": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						Default:             stringdefault.StaticString(defaultDefaultInferenceGeo),
						MarkdownDescription: "Inference geo applied when requests omit it. Must be one of `allowed_inference_geos` unless those are unrestricted. Defaults to `global`.",
					},
				},
			},
			"compartment_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier for the workspace's encryption compartment, referenced by a CMEK key policy.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the workspace was created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *workspaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *workspaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan workspaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tags, d := mapStrings(ctx, plan.Tags)
	resp.Diagnostics.Append(d...)
	residency, d := dataResidencyFromObject(ctx, plan.DataResidency)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.OrganizationWorkspaceNewParams{
		Name:          plan.Name.ValueString(),
		DataResidency: residency.createParam(),
	}
	if len(tags) > 0 {
		params.Tags = tags
	}
	if isConfigured(plan.DisplayColor) {
		params.DisplayColor = anthropic.String(plan.DisplayColor.ValueString())
	}
	if isConfigured(plan.ExternalKeyId) {
		params.ExternalKeyID = anthropic.String(plan.ExternalKeyId.ValueString())
	}

	workspace, err := r.client.Organization.Workspaces.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to create workspace: %s", err))
		return
	}

	resp.Diagnostics.Append(applyWorkspace(ctx, &plan, workspace)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *workspaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state workspaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspace, err := r.client.Organization.Workspaces.Get(ctx, state.Id.ValueString())
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read workspace: %s", err))
		return
	}
	if !workspace.ArchivedAt.IsZero() {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(applyWorkspace(ctx, &state, workspace)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *workspaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state workspaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tags, d := mapStrings(ctx, plan.Tags)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.OrganizationWorkspaceUpdateParams{
		Name: anthropic.String(plan.Name.ValueString()),
		Tags: tags,
	}
	if isConfigured(plan.DisplayColor) {
		params.DisplayColor = anthropic.String(plan.DisplayColor.ValueString())
	}
	if state.ExternalKeyId.IsNull() && isConfigured(plan.ExternalKeyId) {
		params.ExternalKeyID = anthropic.String(plan.ExternalKeyId.ValueString())
	}
	if !plan.DataResidency.Equal(state.DataResidency) {
		residency, d := dataResidencyFromObject(ctx, plan.DataResidency)
		resp.Diagnostics.Append(d...)
		if resp.Diagnostics.HasError() {
			return
		}
		params.DataResidency = residency.updateParam()
	}

	workspace, err := r.client.Organization.Workspaces.Update(ctx, state.Id.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to update workspace: %s", err))
		return
	}

	planned := plan.Tags
	resp.Diagnostics.Append(applyWorkspace(ctx, &plan, workspace)...)
	if !plan.Tags.Equal(planned) {
		resp.Diagnostics.AddAttributeError(path.Root("tags"), "API Error",
			fmt.Sprintf("The Admin API returned tags %s after an update that set %s.", plan.Tags, planned))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *workspaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state workspaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.Id.ValueString()
	workspace, err := r.client.Organization.Workspaces.Get(ctx, id)
	if isNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read workspace: %s", err))
		return
	}
	if !workspace.ArchivedAt.IsZero() {
		return
	}

	if _, err := r.client.Organization.Workspaces.Archive(ctx, id); err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to archive workspace: %s", err))
		return
	}
}

func (r *workspaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func applyWorkspace(ctx context.Context, model *workspaceResourceModel, workspace *anthropic.Workspace) diag.Diagnostics {
	var diags diag.Diagnostics

	model.Id = types.StringValue(workspace.ID)
	model.Name = types.StringValue(workspace.Name)
	model.DisplayColor = types.StringValue(workspace.DisplayColor)
	model.ExternalKeyId = optionalString(workspace.ExternalKeyID)
	model.CompartmentId = types.StringValue(workspace.CompartmentID)
	model.CreatedAt = timestampValue(workspace.CreatedAt)

	tags := workspace.Tags
	if tags == nil {
		tags = map[string]string{}
	}
	tagMap, d := types.MapValueFrom(ctx, types.StringType, tags)
	diags.Append(d...)
	model.Tags = tagMap

	residency, d := dataResidencyFromAPI(workspace.DataResidency).objectValue(ctx)
	diags.Append(d...)
	model.DataResidency = residency

	return diags
}
