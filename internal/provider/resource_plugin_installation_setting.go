package provider

import (
	"context"
	"fmt"
	"regexp"
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

const pluginOrganizationTarget = "organization"

var pluginTargetPattern = regexp.MustCompile(`^(organization|rbac_group_\S+)$`)

var (
	_ resource.Resource                = &pluginInstallationSettingResource{}
	_ resource.ResourceWithImportState = &pluginInstallationSettingResource{}
)

func newPluginInstallationSetting() resource.Resource { return &pluginInstallationSettingResource{} }

type pluginInstallationSettingResource struct {
	client *anthropic.Client
}

type pluginInstallationSettingResourceModel struct {
	Id                     types.String `tfsdk:"id"`
	PluginId               types.String `tfsdk:"plugin_id"`
	Target                 types.String `tfsdk:"target"`
	InstallationPreference types.String `tfsdk:"installation_preference"`
	CreatedAt              types.String `tfsdk:"created_at"`
	UpdatedAt              types.String `tfsdk:"updated_at"`
}

func (r *pluginInstallationSettingResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugin_installation_setting"
}

func (r *pluginInstallationSettingResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages who may use an organization-owned plugin, for the whole organization or for one group, through the Plugins API beta ` + "`ce-plugins-2026-09-01`" + `.

The Plugins API is available only to Claude Enterprise organizations, and not
with HIPAA readiness enabled. It accepts an API key created in claude.ai under
Organization settings > API (` + "`sk-ant-api01-...`" + `), not an Admin API key.
Reads need the ` + "`read:plugins`" + ` scope and writes need ` + "`write:plugins`" + `,
which does not grant reads, so the key needs both.

A ` + "`target`" + ` of ` + "`organization`" + ` sets the plugin's own organization-wide
setting, which stops the plugin inheriting its marketplace's default even when
the value is the same. A group's ` + "`rbac_group_...`" + ` ID sets that group's
setting, which replaces the organization-wide value for its members.

The API upserts on set, so creating this resource refuses to take over a
setting the target already holds; import that setting instead. Destroying this
resource removes the setting: for ` + "`organization`" + ` the plugin inherits its
marketplace's default again, and a group's members fall back to the
organization-wide value. A setting removed outside Terraform is created again
on the next apply. Send writes for one plugin from one configuration: the API
can answer concurrent writes for the same plugin with ` + "`503`" + `, which the
provider retries.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<plugin_id>/<target>`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"plugin_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of an organization-owned plugin. A member-owned plugin has shares instead and returns `404`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"target": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Whose setting this is: `organization`, or the `rbac_group_...` ID of a group the organization can see in `/v1/organizations/rbac_groups`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(pluginTargetPattern, "must be `organization` or an `rbac_group_` ID"),
				},
			},
			"installation_preference": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "What members of the target get: `required` (installed and cannot be removed), `auto_install` (installed and can be removed), `available` (installed on request) or `not_available` (hidden). The API validates the value, so a value it adds later works without a provider release.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the target was first given a setting for this plugin.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the setting last changed.",
			},
		},
	}
}

func (r *pluginInstallationSettingResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *pluginInstallationSettingResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan pluginInstallationSettingResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pluginId := plan.PluginId.ValueString()
	target := plan.Target.ValueString()

	existing, err := r.find(ctx, pluginId, target)
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to check for an existing plugin installation setting: %s", err))
		return
	}
	if existing != nil {
		resp.Diagnostics.AddError("Installation Setting Already Exists",
			fmt.Sprintf("Target %s already holds an installation setting for plugin %s. Import it instead: terraform import anthropic_plugin_installation_setting.<name> %s/%s", target, pluginId, pluginId, target))
		return
	}

	if err := r.set(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to set plugin installation setting: %s", err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *pluginInstallationSettingResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state pluginInstallationSettingResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	setting, err := r.find(ctx, state.PluginId.ValueString(), state.Target.ValueString())
	if isNotFound(err) || (err == nil && setting == nil) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read plugin installation setting: %s", err))
		return
	}

	applyPluginInstallationSetting(&state, setting)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *pluginInstallationSettingResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan pluginInstallationSettingResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.set(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to set plugin installation setting: %s", err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *pluginInstallationSettingResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state pluginInstallationSettingResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.Beta.Organization.Plugins.InstallationSettings.Remove(ctx, state.Target.ValueString(), anthropic.BetaOrganizationPluginInstallationSettingRemoveParams{
		PluginID: state.PluginId.ValueString(),
	})
	if err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to remove plugin installation setting: %s", err))
	}
}

func (r *pluginInstallationSettingResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	pluginId, target, ok := strings.Cut(req.ID, "/")
	if !ok || pluginId == "" || !pluginTargetPattern.MatchString(target) {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected an import ID in the form <plugin_id>/organization or <plugin_id>/<rbac_group_id>, got: %s", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("plugin_id"), pluginId)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("target"), target)...)
}

func (r *pluginInstallationSettingResource) set(ctx context.Context, model *pluginInstallationSettingResourceModel) error {
	setting, err := r.client.Beta.Organization.Plugins.InstallationSettings.Set(ctx, model.Target.ValueString(), anthropic.BetaOrganizationPluginInstallationSettingSetParams{
		PluginID:               model.PluginId.ValueString(),
		InstallationPreference: anthropic.BetaOrganizationPluginInstallationSettingSetParamsInstallationPreference(model.InstallationPreference.ValueString()),
	})
	if err != nil {
		return err
	}
	applyPluginInstallationSetting(model, setting)
	return nil
}

// find lists because the API has no per-target get. A nil setting with a nil
// error means the target holds no setting of its own.
func (r *pluginInstallationSettingResource) find(ctx context.Context, pluginId, target string) (*anthropic.BetaPluginInstallationSetting, error) {
	targetType := anthropic.BetaOrganizationPluginInstallationSettingListParamsTargetTypeRBACGroup
	if target == pluginOrganizationTarget {
		targetType = anthropic.BetaOrganizationPluginInstallationSettingListParamsTargetTypeOrganization
	}
	pager := r.client.Beta.Organization.Plugins.InstallationSettings.ListAutoPaging(ctx, pluginId, anthropic.BetaOrganizationPluginInstallationSettingListParams{
		Limit:      anthropic.Int(100),
		TargetType: targetType,
	})
	for pager.Next() {
		setting := pager.Current()
		if pluginInstallationSettingTarget(setting.Target) == target {
			return &setting, nil
		}
	}
	return nil, pager.Err()
}

func pluginInstallationSettingTarget(target anthropic.BetaPluginInstallationSettingTargetUnion) string {
	if target.Type == "rbac_group" {
		return target.RBACGroupID
	}
	return target.Type
}

func applyPluginInstallationSetting(model *pluginInstallationSettingResourceModel, setting *anthropic.BetaPluginInstallationSetting) {
	target := pluginInstallationSettingTarget(setting.Target)
	model.Id = types.StringValue(setting.PluginID + "/" + target)
	model.PluginId = types.StringValue(setting.PluginID)
	model.Target = types.StringValue(target)
	model.InstallationPreference = types.StringValue(string(setting.InstallationPreference))
	model.CreatedAt = timestampValue(setting.CreatedAt)
	model.UpdatedAt = timestampValue(setting.UpdatedAt)
}
