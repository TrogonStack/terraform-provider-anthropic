package provider

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const pluginMarketplaceImportOnlyMessage = "The Plugins API cannot create plugin marketplaces. Create or connect the marketplace in claude.ai, then import it: terraform import anthropic_plugin_marketplace.<name> marketplace_..."

var (
	_ resource.Resource                = &pluginMarketplaceResource{}
	_ resource.ResourceWithImportState = &pluginMarketplaceResource{}
	_ resource.ResourceWithModifyPlan  = &pluginMarketplaceResource{}
)

func newPluginMarketplace() resource.Resource { return &pluginMarketplaceResource{} }

type pluginMarketplaceResource struct {
	client *anthropic.Client
}

// pluginOwnerModel is shared by the marketplace owner and the owner of each
// plugin in anthropic_plugins, since the API uses one shape for both.
type pluginOwnerModel struct {
	Type   types.String `tfsdk:"type"`
	UserId types.String `tfsdk:"user_id"`
}

type pluginMarketplaceResourceModel struct {
	Id                            types.String      `tfsdk:"id"`
	Name                          types.String      `tfsdk:"name"`
	Owner                         *pluginOwnerModel `tfsdk:"owner"`
	Source                        types.String      `tfsdk:"source"`
	SyncStatus                    types.String      `tfsdk:"sync_status"`
	LastSyncEndedAt               types.String      `tfsdk:"last_sync_ended_at"`
	LastSyncReadSha               types.String      `tfsdk:"last_sync_read_sha"`
	DefaultInstallationPreference types.String      `tfsdk:"default_installation_preference"`
	CreatedAt                     types.String      `tfsdk:"created_at"`
}

func pluginOwner(ownerType, userId string) *pluginOwnerModel {
	owner := &pluginOwnerModel{Type: types.StringValue(ownerType), UserId: types.StringNull()}
	if ownerType == "user" {
		owner.UserId = types.StringValue(userId)
	}
	return owner
}

func (r *pluginMarketplaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugin_marketplace"
}

func (r *pluginMarketplaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Adopts an organization's plugin marketplace through the Plugins API beta ` + "`ce-plugins-2026-09-01`" + `, to manage its default installation setting.

Marketplaces are created, connected to a repository and deleted only in
claude.ai, so this resource is import-only: run
` + "`terraform import anthropic_plugin_marketplace.<name> marketplace_...`" + `
to bring one under management. A plain ` + "`resource \"anthropic_plugin_marketplace\"`" + `
block applied without an import fails, because there is nothing to create.
Destroying this resource only stops managing the marketplace and leaves it and
its default unchanged.

The default applies to every plugin in the marketplace that has no
organization-wide setting of its own, including plugins added later. Once a
marketplace has a default it keeps one: it can be changed but never removed,
so removing ` + "`default_installation_preference`" + ` from the configuration
leaves the current value in place. A member's personal marketplace returns
` + "`403`" + ` on update.

The Plugins API is available only to Claude Enterprise organizations. It
accepts an API key created in claude.ai under Organization settings > API
(` + "`sk-ant-api01-...`" + `) with the ` + "`read:plugins`" + ` and ` + "`write:plugins`" + `
scopes, not an Admin API key.`,
		Attributes: map[string]schema.Attribute{
			"id": rsId(),
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Name of the marketplace.",
			},
			"owner": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Who owns the marketplace: the organization, or the member whose personal marketplace it is.",
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.UseStateForUnknown(),
				},
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Owner type: `organization` or `user`.",
					},
					"user_id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "ID of the member who owns the marketplace. Null unless `type` is `user`.",
					},
				},
			},
			"source": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Where the marketplace's plugins come from: `directory`, `github`, `gitlab`, `manual` or `public_git`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"sync_status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Result of the last synchronization, for example `success`, `in_progress` or `failed_auth`. Null for a `manual` marketplace.",
			},
			"last_sync_ended_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the last synchronization ended. Null when the marketplace has none.",
			},
			"last_sync_read_sha": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Commit the last synchronization read. Null when the marketplace has none.",
			},
			"default_installation_preference": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Default installation setting for plugins in this marketplace: `required`, `auto_install`, `available` or `not_available`. A marketplace whose default was never set reports `not_available`. Leave unset to keep whatever default the marketplace already has. The API validates the value, so a value it adds later works without a provider release.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the marketplace was created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *pluginMarketplaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *pluginMarketplaceResource) ModifyPlan(_ context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() && !req.Plan.Raw.IsNull() {
		resp.Diagnostics.AddError("Plugin Marketplaces Cannot Be Created", pluginMarketplaceImportOnlyMessage)
	}
}

func (r *pluginMarketplaceResource) Create(_ context.Context, _ resource.CreateRequest, resp *resource.CreateResponse) {
	resp.Diagnostics.AddError("Plugin Marketplaces Cannot Be Created", pluginMarketplaceImportOnlyMessage)
}

func (r *pluginMarketplaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state pluginMarketplaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	marketplace, err := r.client.Beta.Organization.PluginMarketplaces.Get(ctx, state.Id.ValueString(), anthropic.BetaOrganizationPluginMarketplaceGetParams{})
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read plugin marketplace: %s", err))
		return
	}

	model := pluginMarketplaceFromAPI(marketplace)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *pluginMarketplaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state pluginMarketplaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var marketplace *anthropic.BetaPluginMarketplace
	var err error
	if isConfigured(plan.DefaultInstallationPreference) && !plan.DefaultInstallationPreference.Equal(state.DefaultInstallationPreference) {
		marketplace, err = r.client.Beta.Organization.PluginMarketplaces.Update(ctx, state.Id.ValueString(), anthropic.BetaOrganizationPluginMarketplaceUpdateParams{
			DefaultInstallationPreference: anthropic.BetaOrganizationPluginMarketplaceUpdateParamsDefaultInstallationPreference(plan.DefaultInstallationPreference.ValueString()),
		})
	} else {
		marketplace, err = r.client.Beta.Organization.PluginMarketplaces.Get(ctx, state.Id.ValueString(), anthropic.BetaOrganizationPluginMarketplaceGetParams{})
	}
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to update plugin marketplace: %s", err))
		return
	}

	model := pluginMarketplaceFromAPI(marketplace)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

// Delete only forgets the marketplace: the API has no marketplace delete, and
// a default cannot be removed once set.
func (r *pluginMarketplaceResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

func (r *pluginMarketplaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func pluginMarketplaceFromAPI(marketplace *anthropic.BetaPluginMarketplace) pluginMarketplaceResourceModel {
	return pluginMarketplaceResourceModel{
		Id:                            types.StringValue(marketplace.ID),
		Name:                          types.StringValue(marketplace.Name),
		Owner:                         pluginOwner(marketplace.Owner.Type, marketplace.Owner.UserID),
		Source:                        types.StringValue(string(marketplace.Source)),
		SyncStatus:                    optionalString(string(marketplace.SyncStatus)),
		LastSyncEndedAt:               timestampValue(marketplace.LastSyncEndedAt),
		LastSyncReadSha:               optionalString(marketplace.LastSyncReadSha),
		DefaultInstallationPreference: types.StringValue(string(marketplace.DefaultInstallationPreference)),
		CreatedAt:                     timestampValue(marketplace.CreatedAt),
	}
}
