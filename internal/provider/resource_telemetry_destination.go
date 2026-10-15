package provider

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

const (
	telemetryWorkspaceBodyLimit = 100
	telemetryWorkspaceMaxList   = 1000
)

var (
	_ resource.Resource                   = &telemetryDestinationResource{}
	_ resource.ResourceWithImportState    = &telemetryDestinationResource{}
	_ resource.ResourceWithValidateConfig = &telemetryDestinationResource{}
)

func newTelemetryDestination() resource.Resource { return &telemetryDestinationResource{} }

type telemetryDestinationResource struct {
	client *anthropic.Client
}

// telemetryDestinationModel is shared by anthropic_telemetry_destination and
// the telemetry_destinations element of anthropic_telemetry_destinations,
// since both expose the same attributes for one destination.
type telemetryDestinationModel struct {
	Id             types.String `tfsdk:"id"`
	EndpointURL    types.String `tfsdk:"endpoint_url"`
	DisplayName    types.String `tfsdk:"display_name"`
	HeaderNames    types.Set    `tfsdk:"header_names"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	DisabledReason types.String `tfsdk:"disabled_reason"`
	Traces         types.Object `tfsdk:"traces"`
	Workspaces     types.Object `tfsdk:"workspaces"`
	DeliveryHealth types.Object `tfsdk:"delivery_health"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
}

type telemetryDestinationResourceModel struct {
	telemetryDestinationModel
	Headers types.Map `tfsdk:"headers"`
}

var telemetryTracesAttrTypes = map[string]attr.Type{
	"content_mode":      types.StringType,
	"enabled":           types.BoolType,
	"endpoint_url":      types.StringType,
	"max_content_bytes": types.Int64Type,
}

var telemetryWorkspacesAttrTypes = map[string]attr.Type{
	"type":          types.StringType,
	"workspace_ids": types.SetType{ElemType: types.StringType},
}

var telemetryDeliveryHealthAttrTypes = map[string]attr.Type{
	"last_error":      types.StringType,
	"last_error_at":   types.StringType,
	"last_success_at": types.StringType,
}

type telemetryTracesModel struct {
	ContentMode     types.String `tfsdk:"content_mode"`
	Enabled         types.Bool   `tfsdk:"enabled"`
	EndpointURL     types.String `tfsdk:"endpoint_url"`
	MaxContentBytes types.Int64  `tfsdk:"max_content_bytes"`
}

type telemetryWorkspacesModel struct {
	Type         types.String `tfsdk:"type"`
	WorkspaceIds types.Set    `tfsdk:"workspace_ids"`
}

// telemetryWorkspaces is the provider's view of which workspaces a
// destination covers. ids is sorted and empty for the "all" type.
type telemetryWorkspaces struct {
	kind string
	ids  []string
}

func telemetryWorkspacesFromObject(ctx context.Context, obj types.Object) (telemetryWorkspaces, diag.Diagnostics) {
	var model telemetryWorkspacesModel
	diags := obj.As(ctx, &model, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return telemetryWorkspaces{}, diags
	}
	ids, d := setStrings(ctx, model.WorkspaceIds)
	diags.Append(d...)
	return telemetryWorkspaces{kind: model.Type.ValueString(), ids: ids}, diags
}

func (w telemetryWorkspaces) param(ids []string) anthropic.BetaTelemetryDestinationWorkspacesParamsUnion {
	switch w.kind {
	case "include":
		return anthropic.BetaTelemetryDestinationWorkspacesParamsUnion{
			OfInclude: &anthropic.BetaTelemetryDestinationWorkspacesIncludeParams{WorkspaceIDs: ids},
		}
	case "exclude":
		return anthropic.BetaTelemetryDestinationWorkspacesParamsUnion{
			OfExclude: &anthropic.BetaTelemetryDestinationWorkspacesExcludeParams{WorkspaceIDs: ids},
		}
	}
	all := anthropic.NewBetaTelemetryDestinationWorkspacesAllParams()
	return anthropic.BetaTelemetryDestinationWorkspacesParamsUnion{OfAll: &all}
}

func splitWorkspaceIDs(ids []string) (head, tail []string) {
	if len(ids) <= telemetryWorkspaceBodyLimit {
		return ids, nil
	}
	return ids[:telemetryWorkspaceBodyLimit], ids[telemetryWorkspaceBodyLimit:]
}

func setStrings(ctx context.Context, v types.Set) ([]string, diag.Diagnostics) {
	if v.IsNull() || v.IsUnknown() {
		return nil, nil
	}
	var out []string
	diags := v.ElementsAs(ctx, &out, false)
	sort.Strings(out)
	return out, diags
}

func sortedStringSet(ctx context.Context, values []string) (types.Set, diag.Diagnostics) {
	sorted := append([]string{}, values...)
	sort.Strings(sorted)
	return types.SetValueFrom(ctx, types.StringType, sorted)
}

func lowerSet(names []string) map[string]struct{} {
	out := make(map[string]struct{}, len(names))
	for _, n := range names {
		out[strings.ToLower(n)] = struct{}{}
	}
	return out
}

func endpointAuthority(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return strings.ToLower(u.Hostname()) + ":" + port
}

func telemetryEnabledParam(enabled bool) anthropic.BetaTelemetryDestinationStateParamsUnion {
	if enabled {
		state := anthropic.NewBetaTelemetryDestinationStateEnabledParams()
		return anthropic.BetaTelemetryDestinationStateParamsUnion{OfEnabled: &state}
	}
	state := anthropic.NewBetaTelemetryDestinationStateDisabledParams()
	return anthropic.BetaTelemetryDestinationStateParamsUnion{OfDisabled: &state}
}

// telemetryDestinationFromAPI maps a destination returned by the Admin API
// into the model shared by the resource and the data source. The API does not
// return workspace IDs with the destination, so the caller lists them
// separately and passes them in.
func telemetryDestinationFromAPI(ctx context.Context, dest anthropic.BetaTelemetryDestination, workspaceIDs []string) (telemetryDestinationModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	model := telemetryDestinationModel{
		Id:          types.StringValue(dest.ID),
		EndpointURL: types.StringValue(dest.EndpointURL),
		DisplayName: optionalString(dest.DisplayName),
		Enabled:     types.BoolValue(dest.State.Type != "disabled"),
		CreatedAt:   timestampValue(dest.CreatedAt),
		UpdatedAt:   timestampValue(dest.UpdatedAt),
	}

	model.DisabledReason = types.StringNull()
	if dest.State.Type == "disabled" {
		model.DisabledReason = types.StringValue(dest.State.Reason.Type)
	}

	names, d := sortedStringSet(ctx, dest.HeaderNames)
	diags.Append(d...)
	model.HeaderNames = names

	endpointURL := types.StringNull()
	if dest.Traces.JSON.EndpointURL.Valid() && dest.Traces.EndpointURL != "" {
		endpointURL = types.StringValue(dest.Traces.EndpointURL)
	}
	maxContentBytes := types.Int64Null()
	if dest.Traces.JSON.MaxContentBytes.Valid() {
		maxContentBytes = types.Int64Value(dest.Traces.MaxContentBytes)
	}
	traces, d := types.ObjectValue(telemetryTracesAttrTypes, map[string]attr.Value{
		"content_mode":      types.StringValue(string(dest.Traces.ContentMode)),
		"enabled":           types.BoolValue(dest.Traces.Enabled),
		"endpoint_url":      endpointURL,
		"max_content_bytes": maxContentBytes,
	})
	diags.Append(d...)
	model.Traces = traces

	ids := types.SetNull(types.StringType)
	if len(workspaceIDs) > 0 {
		ids, d = sortedStringSet(ctx, workspaceIDs)
		diags.Append(d...)
	}
	workspaces, d := types.ObjectValue(telemetryWorkspacesAttrTypes, map[string]attr.Value{
		"type":          types.StringValue(dest.Workspaces.Type),
		"workspace_ids": ids,
	})
	diags.Append(d...)
	model.Workspaces = workspaces

	health := dest.DeliveryHealth
	lastError := types.StringNull()
	if health.JSON.LastError.Valid() {
		lastError = optionalString(health.LastError)
	}
	lastErrorAt := types.StringNull()
	if health.JSON.LastErrorAt.Valid() {
		lastErrorAt = timestampValue(health.LastErrorAt)
	}
	lastSuccessAt := types.StringNull()
	if health.JSON.LastSuccessAt.Valid() {
		lastSuccessAt = timestampValue(health.LastSuccessAt)
	}
	deliveryHealth, d := types.ObjectValue(telemetryDeliveryHealthAttrTypes, map[string]attr.Value{
		"last_error":      lastError,
		"last_error_at":   lastErrorAt,
		"last_success_at": lastSuccessAt,
	})
	diags.Append(d...)
	model.DeliveryHealth = deliveryHealth

	return model, diags
}

func listTelemetryDestinationWorkspaceIDs(ctx context.Context, client *anthropic.Client, destinationID string) ([]string, error) {
	ids := []string{}
	pager := client.Beta.Organization.TelemetryDestinations.Workspaces.ListAutoPaging(ctx, destinationID,
		anthropic.BetaOrganizationTelemetryDestinationWorkspaceListParams{Limit: anthropic.Int(1000)})
	for pager.Next() {
		ids = append(ids, pager.Current().WorkspaceID)
	}
	return ids, pager.Err()
}

func (r *telemetryDestinationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_telemetry_destination"
}

func (r *telemetryDestinationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages an OpenTelemetry telemetry destination through the Admin API.

A telemetry destination is an OTLP/HTTP endpoint that the API exports the
spans of an organization's workspaces to. This uses the Admin API beta
` + "`telemetry-destinations-2026-08-11`" + `, which the provider sends on every
request. An organization can have at most 10 destinations, including disabled
ones.

Destroying this resource deletes the destination, which cannot be undone. The
API stops sending spans to it. A destination deleted outside Terraform is
removed from state and created again on the next apply.

Header values are write-only: the API stores them encrypted and never returns
them, so Terraform keeps the values from the configuration in state and
compares only header names against the API. When any header changes, the
provider sends every configured header again, because the API counts each
stored value it is not sent as 4,096 bytes toward its 16,384-byte limit.
Changing the hostname or port of ` + "`endpoint_url`" + ` makes the API remove every
stored header, so the provider sends every configured header again in the same
request. Headers added outside Terraform are removed the next time this
resource is updated, and a configured header removed outside Terraform shows up
as a change that sets it again.

The API can disable a destination on its own, for example after sustained
delivery failures. ` + "`enabled`" + ` then reads ` + "`false`" + ` and ` + "`disabled_reason`" + ` says
why, so a configuration with ` + "`enabled = true`" + ` plans to enable it again.

If another request changes the destination while an update runs, the API
rejects the update with a conflict. Run ` + "`terraform apply`" + ` again.`,
		Attributes: map[string]schema.Attribute{
			"id": rsId(),
			"endpoint_url": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The base URL of the collector's OTLP/HTTP endpoint, for example `https://gateway.example.com/otlp`. It must use `https` and a hostname, not an IP address, with port 443 if a port is given, and no query string, fragment, or credentials. At most 2,048 characters. A path prefix is allowed, but not the per-signal path (`/v1/traces`), which the API appends on delivery; set `traces.endpoint_url` for a collector that takes traces on another path. Changing the hostname or port makes the API remove every stored header, which the provider repairs by sending the configured `headers` again.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 2048),
				},
			},
			"display_name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Human-readable name for the destination, 1 to 255 characters. Removing it from the configuration clears it.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 255),
				},
			},
			"headers": schema.MapAttribute{
				Optional:            true,
				Sensitive:           true,
				ElementType:         types.StringType,
				MarkdownDescription: "Request headers the API sends with every delivery, typically the collector's authentication header such as `authorization`. At most 20 headers. Names are at most 128 characters and two names cannot differ only in case. Values are printable ASCII, at most 4,096 characters, and cannot begin or end with a space. Names and values together are at most 16,384 bytes. Reserved names such as `host`, `cookie`, `user-agent`, and names starting with `content-`, `proxy-`, `x-forwarded-` or `anthropic-` are rejected. Values are write-only in the API, so Terraform keeps the configured values in state and drops a header from state when its name disappears from the API, which plans a change that sets it again. Removing a header from the configuration removes it from the destination.",
				Validators: []validator.Map{
					mapvalidator.SizeAtMost(20),
				},
			},
			"header_names": schema.SetAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Names of the request headers registered on the destination, as the API reports them.",
			},
			"enabled": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(true),
				MarkdownDescription: "Whether the destination receives deliveries. Defaults to `true`. A destination that the API disabled on its own reads `false`, so leaving this at `true` plans to enable it again. See `disabled_reason`.",
			},
			"disabled_reason": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Why the destination is disabled: `caller` when a caller disabled it, or `sustained_failure`, `invalid_address` or `redirect` when the API did. Null while the destination is enabled.",
			},
			"traces": schema.SingleNestedAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Settings for the traces the destination receives. When unset, every setting takes the API's default on creation and an existing destination keeps its settings. Only the settings that change are sent on update; `endpoint_url` and `max_content_bytes` are removed from the destination when they are left out of a configured `traces` block.",
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.UseStateForUnknown(),
				},
				Attributes: map[string]schema.Attribute{
					"content_mode": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						MarkdownDescription: "What the exported spans contain: `metadata_only` for identifiers, timing, status and token counts, or `content` for the same metadata plus message and tool content. The API default is `metadata_only`. An organization with a data-handling restriction, such as under HIPAA, cannot choose `content`.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.UseStateForUnknown(),
						},
						Validators: []validator.String{
							stringvalidator.OneOf("metadata_only", "content"),
						},
					},
					"enabled": schema.BoolAttribute{
						Optional:            true,
						Computed:            true,
						MarkdownDescription: "Whether the destination receives traces. The API default is `true`. A destination whose `traces.enabled` is `false` keeps its other settings.",
						PlanModifiers: []planmodifier.Bool{
							boolplanmodifier.UseStateForUnknown(),
						},
					},
					"endpoint_url": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "The full URL of the collector's traces endpoint, for a collector that takes traces on a path other than `/v1/traces`. The API posts traces to it exactly as given. It must have the same scheme, host and port as the destination's `endpoint_url`, and be at most 2,048 characters. When unset, the API posts traces to the destination's `endpoint_url` with `/v1/traces` appended. Changing the destination's hostname or port while this is set requires setting it to a URL with the new hostname and port, or removing it.",
						Validators: []validator.String{
							stringvalidator.LengthBetween(1, 2048),
						},
					},
					"max_content_bytes": schema.Int64Attribute{
						Optional:            true,
						MarkdownDescription: "The most bytes of content one exported span can contain, from 1 up to the API's maximum. The API shortens longer content to this size. It applies once `content_mode` is `content`. When unset, only the API's maximum applies.",
						Validators: []validator.Int64{
							int64validator.AtLeast(1),
						},
					},
				},
			},
			"workspaces": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Which of the organization's workspaces the destination covers. A workspace's spans go to every enabled destination that covers it. The API does not return workspace IDs with the destination, so the provider lists them through the destination's workspace list. A list holds at most 1,000 workspaces, at most 100 IDs go in one request, and the API refuses an update that changes the type or replaces the list while it holds more than 750 workspaces, so remove workspaces until 750 or fewer remain first.",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "`all` covers every workspace, including those created later. `include` covers only the workspaces in `workspace_ids`. `exclude` covers every workspace except those in `workspace_ids`, including those created later. Changing the type replaces the list with `workspace_ids`.",
						Validators: []validator.String{
							stringvalidator.OneOf("all", "include", "exclude"),
						},
					},
					"workspace_ids": schema.SetAttribute{
						Optional:            true,
						ElementType:         types.StringType,
						MarkdownDescription: "IDs of the workspaces on the destination's list (`wrkspc_...`), at most 1,000. Must be unset when `type` is `all`. Leave it unset for an empty list: an `include` destination then covers no workspace and an `exclude` destination covers every workspace. An archived workspace cannot be added to the list, but one already on it can stay.",
						Validators: []validator.Set{
							setvalidator.SizeBetween(1, telemetryWorkspaceMaxList),
						},
					},
				},
			},
			"delivery_health": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The most recent delivery results the API records for the destination. The values can lag behind the latest deliveries and change on their own, so Terraform refreshes them on every read.",
				Attributes: map[string]schema.Attribute{
					"last_error": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "A message describing the most recent failed delivery, such as `timeout` or `HTTP error (status 503)`. Null until a delivery first fails. A later successful delivery does not clear it.",
					},
					"last_error_at": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "RFC 3339 timestamp of the failure `last_error` describes. Null until a delivery first fails.",
					},
					"last_success_at": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "RFC 3339 timestamp of the most recent successful delivery. Null until a delivery first succeeds.",
					},
				},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the destination was created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of the last change to the destination's configuration or state. Changes to `delivery_health` do not change it, and neither does adding or removing a single workspace from the list.",
			},
		},
	}
}

func (r *telemetryDestinationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *telemetryDestinationResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var workspaces types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("workspaces"), &workspaces)...)
	if resp.Diagnostics.HasError() || !isConfigured(workspaces) {
		return
	}
	var model telemetryWorkspacesModel
	resp.Diagnostics.Append(workspaces.As(ctx, &model, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}
	if model.Type.ValueString() == "all" && !model.WorkspaceIds.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("workspaces").AtName("workspace_ids"), "Invalid Workspaces",
			"workspace_ids must be unset when type is \"all\", since a destination that covers every workspace has no list.")
	}
}

func (r *telemetryDestinationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan telemetryDestinationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaces, d := telemetryWorkspacesFromObject(ctx, plan.Workspaces)
	resp.Diagnostics.Append(d...)
	headers, d := mapStrings(ctx, plan.Headers)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	head, tail := splitWorkspaceIDs(workspaces.ids)
	params := anthropic.BetaOrganizationTelemetryDestinationNewParams{
		EndpointURL: plan.EndpointURL.ValueString(),
		Workspaces:  workspaces.param(head),
	}
	if isConfigured(plan.DisplayName) {
		params.DisplayName = anthropic.String(plan.DisplayName.ValueString())
	}
	if len(headers) > 0 {
		params.Headers = headers
	}
	if isConfigured(plan.Traces) {
		var traces telemetryTracesModel
		resp.Diagnostics.Append(plan.Traces.As(ctx, &traces, basetypes.ObjectAsOptions{})...)
		if resp.Diagnostics.HasError() {
			return
		}
		params.Traces = traces.createParam()
	}

	dest, err := r.client.Beta.Organization.TelemetryDestinations.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to create telemetry destination: %s", err))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), dest.ID)...)

	for _, workspaceID := range tail {
		if _, err := r.client.Beta.Organization.TelemetryDestinations.Workspaces.Add(ctx, workspaceID,
			anthropic.BetaOrganizationTelemetryDestinationWorkspaceAddParams{DestinationID: dest.ID}); err != nil {
			resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to add workspace %s to telemetry destination %s: %s", workspaceID, dest.ID, err))
			return
		}
	}
	if !plan.Enabled.ValueBool() {
		if _, err := r.client.Beta.Organization.TelemetryDestinations.Update(ctx, dest.ID, anthropic.BetaOrganizationTelemetryDestinationUpdateParams{
			State: telemetryEnabledParam(false),
		}); err != nil {
			resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to disable telemetry destination %s: %s", dest.ID, err))
			return
		}
	}

	model, found, d := r.fetch(ctx, dest.ID)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Telemetry destination %s was not found right after it was created.", dest.ID))
		return
	}
	plan.telemetryDestinationModel = model
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *telemetryDestinationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state telemetryDestinationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	model, found, d := r.fetch(ctx, state.Id.ValueString())
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}

	headers, d := retainHeaders(ctx, state.Headers, model.HeaderNames)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.telemetryDestinationModel = model
	state.Headers = headers
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *telemetryDestinationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state telemetryDestinationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.Id.ValueString()
	params := anthropic.BetaOrganizationTelemetryDestinationUpdateParams{}
	var opts []option.RequestOption
	changed := false

	if !plan.DisplayName.Equal(state.DisplayName) {
		changed = true
		if plan.DisplayName.IsNull() {
			params.DisplayName = param.Null[string]()
		} else {
			params.DisplayName = anthropic.String(plan.DisplayName.ValueString())
		}
	}

	hostChanged := endpointAuthority(plan.EndpointURL.ValueString()) != endpointAuthority(state.EndpointURL.ValueString())
	if !plan.EndpointURL.Equal(state.EndpointURL) {
		changed = true
		params.EndpointURL = anthropic.String(plan.EndpointURL.ValueString())
	}

	planHeaders, d := mapStrings(ctx, plan.Headers)
	resp.Diagnostics.Append(d...)
	storedNames, d := setStrings(ctx, state.HeaderNames)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	var removals []string
	if !hostChanged {
		configured := make([]string, 0, len(planHeaders))
		for name := range planHeaders {
			configured = append(configured, name)
		}
		keep := lowerSet(configured)
		for _, name := range storedNames {
			if _, ok := keep[strings.ToLower(name)]; !ok {
				removals = append(removals, name)
			}
		}
	}
	headersChanged := !plan.Headers.Equal(state.Headers) || len(removals) > 0 || (hostChanged && len(planHeaders) > 0)
	if headersChanged {
		body := make(map[string]any, len(planHeaders)+len(removals))
		for name, value := range planHeaders {
			body[name] = value
		}
		for _, name := range removals {
			body[name] = nil
		}
		if len(body) > 0 {
			changed = true
			params.EndpointURL = anthropic.String(plan.EndpointURL.ValueString())
			opts = append(opts, option.WithJSONSet("headers", body))
		}
	}

	if isConfigured(plan.Traces) && !plan.Traces.Equal(state.Traces) {
		var planTraces, stateTraces telemetryTracesModel
		resp.Diagnostics.Append(plan.Traces.As(ctx, &planTraces, basetypes.ObjectAsOptions{})...)
		resp.Diagnostics.Append(state.Traces.As(ctx, &stateTraces, basetypes.ObjectAsOptions{})...)
		if resp.Diagnostics.HasError() {
			return
		}
		params.Traces = planTraces.updateParam(stateTraces)
		changed = true
	}

	if !plan.Enabled.Equal(state.Enabled) {
		changed = true
		params.State = telemetryEnabledParam(plan.Enabled.ValueBool())
	}

	planWorkspaces, d := telemetryWorkspacesFromObject(ctx, plan.Workspaces)
	resp.Diagnostics.Append(d...)
	stateWorkspaces, d := telemetryWorkspacesFromObject(ctx, state.Workspaces)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	var toAdd, toRemove []string
	if planWorkspaces.kind != stateWorkspaces.kind {
		head, tail := splitWorkspaceIDs(planWorkspaces.ids)
		params.Workspaces = planWorkspaces.param(head)
		toAdd = tail
		changed = true
	} else {
		toAdd = difference(planWorkspaces.ids, stateWorkspaces.ids)
		toRemove = difference(stateWorkspaces.ids, planWorkspaces.ids)
	}

	if changed {
		if _, err := r.client.Beta.Organization.TelemetryDestinations.Update(ctx, id, params, opts...); err != nil {
			if isConflict(err) {
				resp.Diagnostics.AddError("Conflict",
					fmt.Sprintf("Telemetry destination %s was changed by another request while Terraform was updating it. Run terraform apply again to read it and retry. API error: %s", id, err))
				return
			}
			resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to update telemetry destination: %s", err))
			return
		}
	}

	workspaceService := r.client.Beta.Organization.TelemetryDestinations.Workspaces
	for _, workspaceID := range toRemove {
		_, err := workspaceService.Remove(ctx, workspaceID, anthropic.BetaOrganizationTelemetryDestinationWorkspaceRemoveParams{DestinationID: id})
		if err != nil && !isNotFound(err) {
			resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to remove workspace %s from telemetry destination %s: %s", workspaceID, id, err))
			return
		}
	}
	for _, workspaceID := range toAdd {
		if _, err := workspaceService.Add(ctx, workspaceID, anthropic.BetaOrganizationTelemetryDestinationWorkspaceAddParams{DestinationID: id}); err != nil {
			resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to add workspace %s to telemetry destination %s: %s", workspaceID, id, err))
			return
		}
	}

	model, found, d := r.fetch(ctx, id)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Telemetry destination %s was not found right after it was updated.", id))
		return
	}
	plan.telemetryDestinationModel = model
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *telemetryDestinationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state telemetryDestinationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.Beta.Organization.TelemetryDestinations.Delete(ctx, state.Id.ValueString(), anthropic.BetaOrganizationTelemetryDestinationDeleteParams{})
	if err != nil && !isNotFound(err) {
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to delete telemetry destination: %s", err))
		return
	}
}

func (r *telemetryDestinationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// fetch reads a destination and its workspace list, which the API serves
// from a separate endpoint and only for the include and exclude types.
func (r *telemetryDestinationResource) fetch(ctx context.Context, id string) (telemetryDestinationModel, bool, diag.Diagnostics) {
	var diags diag.Diagnostics

	dest, err := r.client.Beta.Organization.TelemetryDestinations.Get(ctx, id, anthropic.BetaOrganizationTelemetryDestinationGetParams{})
	if isNotFound(err) {
		return telemetryDestinationModel{}, false, diags
	}
	if err != nil {
		diags.AddError("API Error", fmt.Sprintf("Unable to read telemetry destination: %s", err))
		return telemetryDestinationModel{}, false, diags
	}

	var workspaceIDs []string
	if dest.Workspaces.Type != "all" {
		workspaceIDs, err = listTelemetryDestinationWorkspaceIDs(ctx, r.client, id)
		if isNotFound(err) {
			return telemetryDestinationModel{}, false, diags
		}
		if err != nil {
			diags.AddError("API Error", fmt.Sprintf("Unable to list the workspaces of telemetry destination %s: %s", id, err))
			return telemetryDestinationModel{}, false, diags
		}
	}

	model, d := telemetryDestinationFromAPI(ctx, *dest, workspaceIDs)
	diags.Append(d...)
	return model, true, diags
}

// retainHeaders drops every configured header whose name the API no longer
// reports, so a header removed outside Terraform shows up as drift. It never
// invents a value, since the API never returns one.
func retainHeaders(ctx context.Context, headers types.Map, names types.Set) (types.Map, diag.Diagnostics) {
	if headers.IsNull() || headers.IsUnknown() {
		return headers, nil
	}
	current, diags := mapStrings(ctx, headers)
	registered, d := setStrings(ctx, names)
	diags.Append(d...)
	if diags.HasError() {
		return headers, diags
	}
	registeredLower := lowerSet(registered)
	kept := map[string]string{}
	for name, value := range current {
		if _, ok := registeredLower[strings.ToLower(name)]; ok {
			kept[name] = value
		}
	}
	out, d := types.MapValueFrom(ctx, types.StringType, kept)
	diags.Append(d...)
	return out, diags
}

func difference(a, b []string) []string {
	var out []string
	for _, v := range a {
		if !slices.Contains(b, v) {
			out = append(out, v)
		}
	}
	return out
}

func (t telemetryTracesModel) createParam() anthropic.BetaTelemetryDestinationTracesParams {
	params := anthropic.BetaTelemetryDestinationTracesParams{}
	if isConfigured(t.ContentMode) {
		params.ContentMode = anthropic.BetaTelemetryDestinationContentMode(t.ContentMode.ValueString())
	}
	if isConfigured(t.Enabled) {
		params.Enabled = anthropic.Bool(t.Enabled.ValueBool())
	}
	if isConfigured(t.EndpointURL) {
		params.EndpointURL = anthropic.String(t.EndpointURL.ValueString())
	}
	if isConfigured(t.MaxContentBytes) {
		params.MaxContentBytes = anthropic.Int(t.MaxContentBytes.ValueInt64())
	}
	return params
}

// updateParam sends only the settings that differ from state. The two
// nullable settings send an explicit null when the plan removes them.
func (t telemetryTracesModel) updateParam(state telemetryTracesModel) anthropic.BetaTelemetryDestinationTracesParams {
	params := anthropic.BetaTelemetryDestinationTracesParams{}
	if isConfigured(t.ContentMode) && !t.ContentMode.Equal(state.ContentMode) {
		params.ContentMode = anthropic.BetaTelemetryDestinationContentMode(t.ContentMode.ValueString())
	}
	if isConfigured(t.Enabled) && !t.Enabled.Equal(state.Enabled) {
		params.Enabled = anthropic.Bool(t.Enabled.ValueBool())
	}
	if !t.EndpointURL.Equal(state.EndpointURL) {
		if t.EndpointURL.IsNull() {
			params.EndpointURL = param.Null[string]()
		} else {
			params.EndpointURL = anthropic.String(t.EndpointURL.ValueString())
		}
	}
	if !t.MaxContentBytes.Equal(state.MaxContentBytes) {
		if t.MaxContentBytes.IsNull() {
			params.MaxContentBytes = param.Null[int64]()
		} else {
			params.MaxContentBytes = anthropic.Int(t.MaxContentBytes.ValueInt64())
		}
	}
	return params
}
