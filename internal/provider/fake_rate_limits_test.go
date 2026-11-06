package provider

import (
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"slices"
)

// Org rate limits come from the Admin API reference examples, plus two
// placeholder groups for a Haiku model group and web search.
const (
	fakeOpusRateLimitID      = "rlt_01NwFawrTvAijwqn8AjKVBPS"
	fakeOpusFourRateLimitID  = "rlt_016qriyigbdebqkzhMujdHkX"
	fakeBatchRateLimitID     = "rlt_01EyY8kq14eviKJPgepbgBH4"
	fakeHaikuRateLimitID     = "rlt_example_haiku"
	fakeWebSearchRateLimitID = "rlt_example_web_search"
)

type fakeOrgRateLimit struct {
	ID          string
	GroupID     string
	GroupType   string
	DisplayName string
	Models      []string
	Limits      map[string]int64
}

func (l fakeOrgRateLimit) toJSON() map[string]any {
	group := map[string]any{"id": l.GroupID, "type": l.GroupType}
	if l.GroupType == "model_group" {
		group["display_name"] = l.DisplayName
	}
	return map[string]any{
		"type":   "rate_limit",
		"id":     l.ID,
		"group":  group,
		"models": l.Models,
		"limits": fakeLimitList(l.Limits),
	}
}

func fakeLimitList(limits map[string]int64) []map[string]any {
	list := []map[string]any{}
	for _, limiterType := range slices.Sorted(maps.Keys(limits)) {
		list = append(list, map[string]any{"type": limiterType, "value": limits[limiterType]})
	}
	return list
}

var fakeSettableLimiterTypes = map[string][]string{
	"model_group": {"requests_per_minute", "input_tokens_per_minute", "output_tokens_per_minute", "input_tokens_per_minute_cache_aware", "tokens_per_minute", "tokens_per_day", "fast_itpmca", "fast_otpm"},
	"web_search":  {"tool_uses_per_second"},
	"batch":       {"requests_per_minute"},
}

func (f *fakeAdminAPI) registerRateLimitRoutes() {
	f.orgRateLimits = []fakeOrgRateLimit{
		{ID: fakeOpusRateLimitID, GroupID: "rlg_01Hq7YkP3mZ9dTwRx4cVbN2s", GroupType: "model_group", DisplayName: "Claude Opus 5.5", Models: []string{"claude-opus-5-5"},
			Limits: map[string]int64{"requests_per_minute": 4000, "input_tokens_per_minute": 10000000, "output_tokens_per_minute": 800000}},
		{ID: fakeOpusFourRateLimitID, GroupID: "rlg_01Kd5wMv8nSq2LcXy6tRfJ4b", GroupType: "model_group", DisplayName: "Claude Opus 4.x", Models: []string{"claude-opus-4-5", "claude-opus-4-5-20251101", "claude-opus-4-6", "claude-opus-4-7", "claude-opus-4-8"},
			Limits: map[string]int64{"requests_per_minute": 4000, "input_tokens_per_minute": 2000000, "output_tokens_per_minute": 400000}},
		{ID: fakeHaikuRateLimitID, GroupID: "rlg_example_haiku", GroupType: "model_group", DisplayName: "Claude Haiku 4.x", Models: []string{"claude-haiku-4-5", "claude-haiku-4-5-20251001"},
			Limits: map[string]int64{"requests_per_minute": 4000, "input_tokens_per_minute": 4000000, "output_tokens_per_minute": 800000, "tokens_per_day": 100000000}},
		{ID: fakeBatchRateLimitID, GroupID: "rlg_01Wn3pBz6kCg9vHtQ7mLxD5a", GroupType: "batch",
			Limits: map[string]int64{"enqueued_batch_requests": 500000}},
		{ID: fakeWebSearchRateLimitID, GroupID: "rlg_example_web_search", GroupType: "web_search",
			Limits: map[string]int64{"tool_uses_per_second": 30}},
	}
	f.workspaceRateLimits = make(map[string]map[string]map[string]int64)
	f.listedWorkspaceRateLimits = make(map[string]map[string]map[string]int64)

	f.mux.HandleFunc("GET /v1/organizations/rate_limits", f.listOrgRateLimits)
	f.mux.HandleFunc("GET /v1/organizations/workspaces/{id}/rate_limits", f.withWorkspace(f.listWorkspaceRateLimits))
	f.mux.HandleFunc("POST /v1/organizations/workspaces/{id}/rate_limits/{rate_limit_id}", f.withWorkspace(f.updateWorkspaceRateLimit))
}

func (f *fakeAdminAPI) orgRateLimit(id string) (fakeOrgRateLimit, bool) {
	for _, limit := range f.orgRateLimits {
		if limit.ID == id {
			return limit, true
		}
	}
	return fakeOrgRateLimit{}, false
}

func (f *fakeAdminAPI) listOrgRateLimits(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	groupType := query.Get("group_type")
	model := query.Get("model")
	matching := []fakeOrgRateLimit{}
	for _, limit := range f.orgRateLimits {
		if groupType != "" && limit.GroupType != groupType {
			continue
		}
		if model != "" && !slices.Contains(limit.Models, model) {
			continue
		}
		matching = append(matching, limit)
	}
	if model != "" && len(matching) == 0 {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "Model not found or has no rate limits")
		return
	}
	fakePage(w, r, matching, func(limit fakeOrgRateLimit) any { return limit.toJSON() })
}

func (f *fakeAdminAPI) workspaceRateLimitJSON(workspaceID, rateLimitID string, overrides map[string]int64) map[string]any {
	org, _ := f.orgRateLimit(rateLimitID)
	group := org.toJSON()["group"]
	limits := []map[string]any{}
	for _, limiterType := range slices.Sorted(maps.Keys(overrides)) {
		var orgLimit any
		if value, ok := org.Limits[limiterType]; ok {
			orgLimit = value
		}
		limits = append(limits, map[string]any{
			"type":      limiterType,
			"value":     overrides[limiterType],
			"org_limit": orgLimit,
			"source":    map[string]any{"type": "workspace"},
		})
	}
	return map[string]any{
		"type":          "workspace_rate_limit",
		"workspace_id":  workspaceID,
		"rate_limit_id": rateLimitID,
		"group":         group,
		"models":        org.Models,
		"limits":        limits,
	}
}

// listWorkspaceRateLimits answers 404 for an archived workspace unless
// rateLimitListServesArchived is set. The API reference does not say which.
func (f *fakeAdminAPI) listWorkspaceRateLimits(w http.ResponseWriter, r *http.Request, ws *fakeWorkspace) {
	if ws.ArchivedAt != nil && !f.rateLimitListServesArchived {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "Workspace not found")
		return
	}
	listed := f.listedWorkspaceRateLimits[ws.ID]
	entries := []string{}
	for _, limit := range f.orgRateLimits {
		if len(listed[limit.ID]) > 0 {
			entries = append(entries, limit.ID)
		}
	}
	fakePage(w, r, entries, func(rateLimitID string) any {
		return f.workspaceRateLimitJSON(ws.ID, rateLimitID, listed[rateLimitID])
	})
}

func (f *fakeAdminAPI) updateWorkspaceRateLimit(w http.ResponseWriter, r *http.Request, ws *fakeWorkspace) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		fakeInvalid(w, "%s", err)
		return
	}
	f.rateLimitUpdateBodies = append(f.rateLimitUpdateBodies, string(raw))

	var body struct {
		Limits []struct {
			Type  string `json:"type"`
			Value *int64 `json:"value"`
		} `json:"limits"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		fakeInvalid(w, "%s", err)
		return
	}
	if ws.ArchivedAt != nil {
		fakeInvalid(w, "Workspace is archived")
		return
	}
	rateLimitID := r.PathValue("rate_limit_id")
	org, ok := f.orgRateLimit(rateLimitID)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "Rate limit not found")
		return
	}
	if len(body.Limits) < 1 || len(body.Limits) > 16 {
		fakeInvalid(w, "limits: must contain between 1 and 16 entries")
		return
	}
	for _, limit := range body.Limits {
		if limit.Value == nil {
			continue
		}
		if !slices.Contains(fakeSettableLimiterTypes[org.GroupType], limit.Type) {
			fakeInvalid(w, "limits: %s cannot be set on a %s group", limit.Type, org.GroupType)
			return
		}
		if *limit.Value < 0 {
			fakeInvalid(w, "limits: value must be at least 0")
			return
		}
	}

	overrides := maps.Clone(f.workspaceRateLimits[ws.ID][rateLimitID])
	if overrides == nil {
		overrides = map[string]int64{}
	}
	for _, limit := range body.Limits {
		if limit.Value == nil {
			delete(overrides, limit.Type)
		} else {
			overrides[limit.Type] = *limit.Value
		}
	}
	setFakeOverrides(f.workspaceRateLimits, ws.ID, rateLimitID, overrides)
	if !f.rateLimitListLags {
		setFakeOverrides(f.listedWorkspaceRateLimits, ws.ID, rateLimitID, maps.Clone(overrides))
	}
	writeFakeJSON(w, http.StatusOK, f.workspaceRateLimitJSON(ws.ID, rateLimitID, overrides))
}

func setFakeOverrides(store map[string]map[string]map[string]int64, workspaceID, rateLimitID string, overrides map[string]int64) {
	if store[workspaceID] == nil {
		store[workspaceID] = map[string]map[string]int64{}
	}
	store[workspaceID][rateLimitID] = overrides
}

// setRateLimitOverride changes an override out of band, as another client
// would, and lists it at once. Callers hold f.mu.
func (f *fakeAdminAPI) setRateLimitOverride(workspaceID, rateLimitID, limiterType string, value int64) {
	overrides := maps.Clone(f.workspaceRateLimits[workspaceID][rateLimitID])
	if overrides == nil {
		overrides = map[string]int64{}
	}
	overrides[limiterType] = value
	setFakeOverrides(f.workspaceRateLimits, workspaceID, rateLimitID, overrides)
	setFakeOverrides(f.listedWorkspaceRateLimits, workspaceID, rateLimitID, maps.Clone(overrides))
}

// catchUpRateLimitList makes the list show every write. Callers hold f.mu.
func (f *fakeAdminAPI) catchUpRateLimitList() {
	for workspaceID, entries := range f.workspaceRateLimits {
		for rateLimitID, overrides := range entries {
			setFakeOverrides(f.listedWorkspaceRateLimits, workspaceID, rateLimitID, maps.Clone(overrides))
		}
	}
}

func (f *fakeAdminAPI) rateLimitOverrides(workspaceID, rateLimitID string) map[string]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.workspaceRateLimits[workspaceID][rateLimitID])
}
