package provider

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &workspaceRateLimitResource{}
	_ resource.ResourceWithImportState    = &workspaceRateLimitResource{}
	_ resource.ResourceWithModifyPlan     = &workspaceRateLimitResource{}
	_ resource.ResourceWithValidateConfig = &workspaceRateLimitResource{}
)

const (
	// maximumRateLimitValue is the largest value the Set Workspace Rate Limits endpoint accepts.
	maximumRateLimitValue = 9007199254740991

	pendingRateLimitWriteKey = "pending_write"

	// rateLimitListLag bounds how long Read trusts a write over the list, which the
	// API documents can lag a write. Changes take effect "within about an hour".
	rateLimitListLag = time.Hour
)

// currentTime is replaced in tests to move past rateLimitListLag.
var currentTime = time.Now

var settableLimiterTypes = []string{
	"requests_per_minute",
	"input_tokens_per_minute",
	"output_tokens_per_minute",
	"input_tokens_per_minute_cache_aware",
	"tokens_per_minute",
	"tokens_per_day",
	"tool_uses_per_second",
	"fast_itpmca",
	"fast_otpm",
}

// limiterTypesByGroupType holds only the group types whose accepted limiter
// types the Rate Limits API documents.
var limiterTypesByGroupType = map[string][]string{
	"model_group": slices.DeleteFunc(slices.Clone(settableLimiterTypes), func(limiterType string) bool {
		return limiterType == "tool_uses_per_second"
	}),
	"web_search": {"tool_uses_per_second"},
	"batch":      {"requests_per_minute"},
}

func newWorkspaceRateLimit() resource.Resource { return &workspaceRateLimitResource{} }

type workspaceRateLimitResource struct {
	client *anthropic.Client
}

type workspaceRateLimitResourceModel struct {
	Id          types.String `tfsdk:"id"`
	WorkspaceId types.String `tfsdk:"workspace_id"`
	RateLimitId types.String `tfsdk:"rate_limit_id"`
	Limits      types.Map    `tfsdk:"limits"`
}

// privateState is the private state of a resource response, which the
// framework holds in an internal package.
type privateState interface {
	GetKey(ctx context.Context, key string) ([]byte, diag.Diagnostics)
	SetKey(ctx context.Context, key string, value []byte) diag.Diagnostics
}

// pendingRateLimitWrite records a write until the list shows it. Stale holds,
// per limiter type, the values a lagging list can still show, with null for
// no override. Read reports any other listed value as drift.
type pendingRateLimitWrite struct {
	WrittenAt time.Time           `json:"written_at"`
	Limits    map[string]int64    `json:"limits"`
	Stale     map[string][]*int64 `json:"stale"`
}

func (r *workspaceRateLimitResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace_rate_limit"
}

func (r *workspaceRateLimitResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a workspace's rate-limit overrides for one rate-limit group through the Admin API.

Each entry in ` + "`limits`" + ` sets the workspace's override for one limiter type
of the group. A limiter type that is not in ` + "`limits`" + ` keeps whatever value it
has, so this resource leaves overrides it does not manage alone. Removing a
type from ` + "`limits`" + ` clears its override, and destroying the resource clears
every override it manages, so the workspace inherits the organization's value
again. Manage each workspace and group pair with one resource only. An
import adopts every override the workspace has for the group.

The API stores a value above the organization's limit for the same limiter
type, but it enforces the lower of the two. A change takes effect within about
an hour. The API applies a change at once in its response, but a list made
right after can still show the previous value. For one hour after a write,
the provider keeps the written value while the list still shows the value from before
the write, so a refresh right after an apply plans no change. Any other value
shows as drift.

Use the ` + "`anthropic_rate_limits`" + ` data source to find a group's ` + "`rate_limit_id`" + `
from a model name. Setting workspace rate limits is not available on Claude
Platform on AWS.`,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<workspace_id>/<rate_limit_id>`.",
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
			"rate_limit_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The top-level `id` of the organization's rate-limit entry for the group, as `anthropic_rate_limits` returns it. This is not the `group_id` inside it. The ID differs between organizations.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"limits": schema.MapAttribute{
				Required:    true,
				ElementType: types.Int64Type,
				MarkdownDescription: "The workspace's override for each limiter type, from `0` to `9007199254740991`, keyed by limiter type. " +
					"The API accepts `requests_per_minute`, `input_tokens_per_minute`, `output_tokens_per_minute`, `input_tokens_per_minute_cache_aware`, `tokens_per_minute`, `tokens_per_day`, `tool_uses_per_second`, `fast_itpmca` and `fast_otpm`. `fast_itpmca` and `fast_otpm` apply only to the groups where `anthropic_rate_limits` shows them. " +
					"A `model_group` accepts every type except `tool_uses_per_second`, a `web_search` group accepts only `tool_uses_per_second`, and a `batch` group accepts only `requests_per_minute`. " +
					"The plan fails when a known type does not suit the group, and warns about a type this provider does not know, which the API can still accept.",
				Validators: []validator.Map{
					mapvalidator.SizeAtLeast(1),
					mapvalidator.ValueInt64sAre(int64validator.Between(0, maximumRateLimitValue)),
				},
			},
		},
	}
}

func (r *workspaceRateLimitResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *workspaceRateLimitResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var limits types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("limits"), &limits)...)
	if resp.Diagnostics.HasError() || !isConfigured(limits) {
		return
	}
	for _, limiterType := range slices.Sorted(maps.Keys(limits.Elements())) {
		if !slices.Contains(settableLimiterTypes, limiterType) {
			resp.Diagnostics.AddAttributeWarning(path.Root("limits").AtMapKey(limiterType), "Unknown Limiter Type",
				fmt.Sprintf("%q is not a limiter type this provider knows: %s. The API rejects a type it does not accept and then changes nothing.", limiterType, strings.Join(settableLimiterTypes, ", ")))
		}
	}
}

// ModifyPlan checks the planned limiter types against the group's type, which
// only the API knows. A failed lookup leaves the check to the API at apply.
func (r *workspaceRateLimitResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.client == nil {
		return
	}
	var plan workspaceRateLimitResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || !isConfigured(plan.RateLimitId) || !isConfigured(plan.Limits) {
		return
	}
	if !req.State.Raw.IsNull() {
		var state workspaceRateLimitResourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() || (plan.RateLimitId.Equal(state.RateLimitId) && plan.Limits.Equal(state.Limits)) {
			return
		}
	}

	groupType, found := r.groupType(ctx, plan.RateLimitId.ValueString())
	accepted, documented := limiterTypesByGroupType[groupType]
	if !found || !documented {
		return
	}
	for _, limiterType := range slices.Sorted(maps.Keys(plan.Limits.Elements())) {
		if slices.Contains(settableLimiterTypes, limiterType) && !slices.Contains(accepted, limiterType) {
			resp.Diagnostics.AddAttributeError(path.Root("limits").AtMapKey(limiterType), "Limiter Type Not Accepted",
				fmt.Sprintf("Rate limit %s is a %s group, which accepts only %s.", plan.RateLimitId.ValueString(), groupType, strings.Join(accepted, ", ")))
		}
	}
}

func (r *workspaceRateLimitResource) groupType(ctx context.Context, rateLimitID string) (string, bool) {
	pager := r.client.Organization.RateLimits.ListAutoPaging(ctx, anthropic.OrganizationRateLimitListParams{})
	for pager.Next() {
		if entry := pager.Current(); entry.ID == rateLimitID {
			return entry.Group.Type, true
		}
	}
	return "", false
}

func (r *workspaceRateLimitResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan workspaceRateLimitResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	desired, diags := rateLimitValues(ctx, plan.Limits)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	listedBefore, listErr := r.listOverrides(ctx, plan.WorkspaceId.ValueString(), plan.RateLimitId.ValueString())
	resp.Diagnostics.Append(r.write(ctx, &plan, desired, nil)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	pending := newPendingRateLimitWrite(desired, pendingRateLimitWrite{}, listedBefore, listErr == nil, nil)
	resp.Diagnostics.Append(setPendingRateLimitWrite(ctx, resp.Private, pending)...)
}

func (r *workspaceRateLimitResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state workspaceRateLimitResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID := state.WorkspaceId.ValueString()
	rateLimitID := state.RateLimitId.ValueString()
	listed, err := r.listOverrides(ctx, workspaceID, rateLimitID)
	if isNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		gone, goneErr := r.workspaceGone(ctx, workspaceID)
		if goneErr == nil && gone {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to read workspace rate limits: %s", err))
		return
	}

	state.Id = types.StringValue(workspaceID + "/" + rateLimitID)
	if state.Limits.IsNull() {
		if len(listed) == 0 {
			resp.State.RemoveResource(ctx)
			return
		}
		state.Limits = rateLimitMap(listed)
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		return
	}

	managed, diags := rateLimitValues(ctx, state.Limits)
	resp.Diagnostics.Append(diags...)
	pending, found, diags := getPendingRateLimitWrite(ctx, req.Private)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	current := map[string]int64{}
	for limiterType := range managed {
		if written, ok := pending.Limits[limiterType]; ok && pending.lagging(limiterType, listed) {
			current[limiterType] = written
		} else if value, ok := listed[limiterType]; ok {
			current[limiterType] = value
		}
	}
	if found && pending.settled(listed) {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, pendingRateLimitWriteKey, nil)...)
	}

	state.Limits = rateLimitMap(current)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *workspaceRateLimitResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state workspaceRateLimitResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	desired, diags := rateLimitValues(ctx, plan.Limits)
	resp.Diagnostics.Append(diags...)
	previous, diags := rateLimitValues(ctx, state.Limits)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var cleared []string
	for limiterType := range previous {
		if _, ok := desired[limiterType]; !ok {
			cleared = append(cleared, limiterType)
		}
	}

	earlier, _, diags := getPendingRateLimitWrite(ctx, req.Private)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	listedBefore, listErr := r.listOverrides(ctx, plan.WorkspaceId.ValueString(), plan.RateLimitId.ValueString())
	resp.Diagnostics.Append(r.write(ctx, &plan, desired, cleared)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	pending := newPendingRateLimitWrite(desired, earlier, listedBefore, listErr == nil, previous)
	resp.Diagnostics.Append(setPendingRateLimitWrite(ctx, resp.Private, pending)...)
}

func (r *workspaceRateLimitResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state workspaceRateLimitResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	managed, diags := rateLimitValues(ctx, state.Limits)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() || len(managed) == 0 {
		return
	}

	workspaceID := state.WorkspaceId.ValueString()
	_, err := r.client.Organization.Workspaces.RateLimits.Update(ctx, state.RateLimitId.ValueString(), anthropic.OrganizationWorkspaceRateLimitUpdateParams{
		WorkspaceID: workspaceID,
		Limits:      rateLimitUpdateValues(nil, slices.Collect(maps.Keys(managed))),
	})
	if err != nil && !isNotFound(err) {
		gone, goneErr := r.workspaceGone(ctx, workspaceID)
		if goneErr == nil && gone {
			return
		}
		resp.Diagnostics.AddError("API Error", fmt.Sprintf("Unable to clear workspace rate limits: %s", err))
	}
}

func (r *workspaceRateLimitResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	workspaceID, rateLimitID, ok := strings.Cut(req.ID, "/")
	if !ok || workspaceID == "" || rateLimitID == "" {
		resp.Diagnostics.AddError("Invalid Import ID",
			fmt.Sprintf("Expected an import ID in the form <workspace_id>/<rate_limit_id>, got: %s", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workspace_id"), workspaceID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("rate_limit_id"), rateLimitID)...)
}

// write keeps the planned values in state and checks them against the
// response, since a list made right after the write can still lag it.
func (r *workspaceRateLimitResource) write(ctx context.Context, plan *workspaceRateLimitResourceModel, desired map[string]int64, cleared []string) diag.Diagnostics {
	var diags diag.Diagnostics
	workspaceID := plan.WorkspaceId.ValueString()
	rateLimitID := plan.RateLimitId.ValueString()

	response, err := r.client.Organization.Workspaces.RateLimits.Update(ctx, rateLimitID, anthropic.OrganizationWorkspaceRateLimitUpdateParams{
		WorkspaceID: workspaceID,
		Limits:      rateLimitUpdateValues(desired, cleared),
	})
	if err != nil {
		diags.AddError("API Error", fmt.Sprintf("Unable to set workspace rate limits: %s", err))
		return diags
	}

	applied := workspaceOverrides(response.Limits)
	for _, limiterType := range slices.Sorted(maps.Keys(desired)) {
		if value, ok := applied[limiterType]; !ok || value != desired[limiterType] {
			diags.AddError("Unexpected API Response",
				fmt.Sprintf("Rate limit %s of workspace %s was set to %d for %s, but the API returned %s.", rateLimitID, workspaceID, desired[limiterType], limiterType, describeOverride(applied, limiterType)))
		}
	}
	for _, limiterType := range cleared {
		if value, ok := applied[limiterType]; ok {
			diags.AddError("Unexpected API Response",
				fmt.Sprintf("Rate limit %s of workspace %s was cleared for %s, but the API still returned an override of %d.", rateLimitID, workspaceID, limiterType, value))
		}
	}
	plan.Id = types.StringValue(workspaceID + "/" + rateLimitID)
	return diags
}

func (r *workspaceRateLimitResource) listOverrides(ctx context.Context, workspaceID, rateLimitID string) (map[string]int64, error) {
	pager := r.client.Organization.Workspaces.RateLimits.ListAutoPaging(ctx, workspaceID, anthropic.OrganizationWorkspaceRateLimitListParams{})
	for pager.Next() {
		if entry := pager.Current(); entry.RateLimitID == rateLimitID {
			return workspaceOverrides(entry.Limits), nil
		}
	}
	if err := pager.Err(); err != nil {
		return nil, err
	}
	return map[string]int64{}, nil
}

func (r *workspaceRateLimitResource) workspaceGone(ctx context.Context, workspaceID string) (bool, error) {
	workspace, err := r.client.Organization.Workspaces.Get(ctx, workspaceID)
	if isNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return !workspace.ArchivedAt.IsZero(), nil
}

func workspaceOverrides(limits []anthropic.WorkspaceRateLimitValue) map[string]int64 {
	overrides := map[string]int64{}
	for _, limit := range limits {
		if limit.Source.Type != "organization" {
			overrides[limit.Type] = limit.Value
		}
	}
	return overrides
}

func describeOverride(overrides map[string]int64, limiterType string) string {
	if value, ok := overrides[limiterType]; ok {
		return fmt.Sprintf("%d", value)
	}
	return "no override"
}

func rateLimitUpdateValues(desired map[string]int64, cleared []string) []anthropic.WorkspaceRateLimitUpdateValueParam {
	values := make([]anthropic.WorkspaceRateLimitUpdateValueParam, 0, len(desired)+len(cleared))
	for _, limiterType := range slices.Sorted(maps.Keys(desired)) {
		values = append(values, anthropic.WorkspaceRateLimitUpdateValueParam{
			Type:  anthropic.WorkspaceRateLimitType(limiterType),
			Value: anthropic.Int(desired[limiterType]),
		})
	}
	for _, limiterType := range slices.Sorted(slices.Values(cleared)) {
		values = append(values, anthropic.WorkspaceRateLimitUpdateValueParam{
			Type:  anthropic.WorkspaceRateLimitType(limiterType),
			Value: param.Null[int64](),
		})
	}
	return values
}

func rateLimitValues(ctx context.Context, limits types.Map) (map[string]int64, diag.Diagnostics) {
	values := map[string]int64{}
	if limits.IsNull() || limits.IsUnknown() {
		return values, nil
	}
	diags := limits.ElementsAs(ctx, &values, false)
	return values, diags
}

func rateLimitMap(values map[string]int64) types.Map {
	elements := make(map[string]attr.Value, len(values))
	for limiterType, value := range values {
		elements[limiterType] = types.Int64Value(value)
	}
	return types.MapValueMust(types.Int64Type, elements)
}

// newPendingRateLimitWrite collects the values a lagging list can still show
// after a write: those an earlier write left pending, those the list showed
// before this write, and those the state held.
func newPendingRateLimitWrite(written map[string]int64, earlier pendingRateLimitWrite, listedBefore map[string]int64, listedBeforeKnown bool, priorState map[string]int64) pendingRateLimitWrite {
	stale := make(map[string][]*int64, len(written))
	for limiterType := range written {
		candidates := slices.Clone(earlier.Stale[limiterType])
		if value, ok := earlier.Limits[limiterType]; ok {
			candidates = append(candidates, &value)
		}
		if value, ok := listedBefore[limiterType]; ok {
			candidates = append(candidates, &value)
		} else if listedBeforeKnown || len(candidates) == 0 {
			candidates = append(candidates, nil)
		}
		if value, ok := priorState[limiterType]; ok {
			candidates = append(candidates, &value)
		}
		stale[limiterType] = slices.CompactFunc(slices.SortedFunc(slices.Values(candidates), compareOptionalInt64), func(a, b *int64) bool {
			return compareOptionalInt64(a, b) == 0
		})
	}
	return pendingRateLimitWrite{WrittenAt: currentTime().UTC(), Limits: written, Stale: stale}
}

func compareOptionalInt64(a, b *int64) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	default:
		return cmp.Compare(*a, *b)
	}
}

func setPendingRateLimitWrite(ctx context.Context, private privateState, pending pendingRateLimitWrite) diag.Diagnostics {
	encoded, err := json.Marshal(pending)
	if err != nil {
		var diags diag.Diagnostics
		diags.AddError("Internal Error", fmt.Sprintf("Unable to record the rate limits just written: %s", err))
		return diags
	}
	return private.SetKey(ctx, pendingRateLimitWriteKey, encoded)
}

// getPendingRateLimitWrite returns an empty write, but reports it found, once
// rateLimitListLag has passed since the write, so Read trusts the list again.
func getPendingRateLimitWrite(ctx context.Context, private privateState) (pendingRateLimitWrite, bool, diag.Diagnostics) {
	encoded, diags := private.GetKey(ctx, pendingRateLimitWriteKey)
	if diags.HasError() || len(encoded) == 0 {
		return pendingRateLimitWrite{}, false, diags
	}
	var pending pendingRateLimitWrite
	if err := json.Unmarshal(encoded, &pending); err != nil || currentTime().Sub(pending.WrittenAt) > rateLimitListLag {
		return pendingRateLimitWrite{}, true, diags
	}
	return pending, true, diags
}

// lagging reports whether the list still shows a value from before the write.
func (p pendingRateLimitWrite) lagging(limiterType string, listed map[string]int64) bool {
	value, ok := listed[limiterType]
	if ok && value == p.Limits[limiterType] {
		return false
	}
	return slices.ContainsFunc(p.Stale[limiterType], func(stale *int64) bool {
		return (stale == nil && !ok) || (stale != nil && ok && *stale == value)
	})
}

// settled reports whether the list now shows every written value.
func (p pendingRateLimitWrite) settled(listed map[string]int64) bool {
	for limiterType, written := range p.Limits {
		if value, ok := listed[limiterType]; !ok || value != written {
			return false
		}
	}
	return true
}
