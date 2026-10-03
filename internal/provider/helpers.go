package provider

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func rsId() schema.StringAttribute {
	return schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: "The unique ID of this resource.",
		PlanModifiers: []planmodifier.String{
			stringplanmodifier.UseStateForUnknown(),
		},
	}
}

func isConfigured(v attr.Value) bool {
	return !v.IsNull() && !v.IsUnknown()
}

func optionalString(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func timestampValue(t time.Time) types.String {
	if t.IsZero() {
		return types.StringNull()
	}
	return types.StringValue(t.UTC().Format(time.RFC3339))
}

// mapStrings returns a non-nil map for a known value, so an empty map still
// reaches the API and clears every tag.
func mapStrings(ctx context.Context, v types.Map) (map[string]string, diag.Diagnostics) {
	if v.IsNull() || v.IsUnknown() {
		return nil, nil
	}
	out := map[string]string{}
	diags := v.ElementsAs(ctx, &out, false)
	return out, diags
}

// writeOnceString rejects a plan that changes or removes a value the API
// never lets go of once set, instead of replacing the resource.
func writeOnceString() planmodifier.String {
	return writeOnceStringModifier{}
}

type writeOnceStringModifier struct{}

func (m writeOnceStringModifier) Description(_ context.Context) string {
	return "Once set, the value cannot be changed or removed."
}

func (m writeOnceStringModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m writeOnceStringModifier) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.State.Raw.IsNull() || req.StateValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if req.ConfigValue.Equal(req.StateValue) {
		return
	}
	resp.Diagnostics.AddAttributeError(req.Path, "Write-Once Attribute",
		fmt.Sprintf("%s is already set to %s and cannot be changed or removed. Set it back to that value.", req.Path, req.StateValue))
}

// withoutPrefix matches any string that does not begin with prefix, spelled
// out per character because Go regexp has no negative lookahead.
func withoutPrefix(prefix string) *regexp.Regexp {
	pattern := ""
	for i := len(prefix) - 1; i >= 0; i-- {
		c := regexp.QuoteMeta(prefix[i : i+1])
		next := ""
		if pattern != "" {
			next = "|" + c + pattern
		}
		pattern = "(?:$|[^" + c + "]" + next + ")"
	}
	return regexp.MustCompile("^" + pattern)
}
