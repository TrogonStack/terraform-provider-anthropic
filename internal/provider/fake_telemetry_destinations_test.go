package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	fakeTelemetryBeta            = "telemetry-destinations-2026-08-11"
	fakeTelemetryPageSize        = 2
	fakeTelemetryMaxDestinations = 10
	fakeTelemetryMaxHeaders      = 20
	fakeTelemetryMaxHeaderBytes  = 16384
	fakeTelemetryUnsetHeaderCost = 4096
	fakeTelemetryBodyIDLimit     = 100
	fakeTelemetryMaxList         = 1000
	fakeTelemetryReplaceLimit    = 750
)

var fakeTelemetryReservedHeaderPrefixes = []string{"content-", "proxy-", "x-forwarded-", "anthropic-"}

var fakeTelemetryReservedHeaders = []string{"host", "cookie", "user-agent"}

type fakeTelemetryDestination struct {
	ID              string
	EndpointURL     string
	DisplayName     *string
	Headers         map[string]string
	Disabled        bool
	DisabledReason  string
	ContentMode     string
	TracesEnabled   bool
	TracesEndpoint  *string
	MaxContentBytes *int64
	WorkspacesType  string
	WorkspaceIDs    []string // newest entry first
	LastError       *string
	LastErrorAt     *string
	LastSuccessAt   *string
	CreatedAt       string
	UpdatedAt       string
	sequence        int
}

func (d *fakeTelemetryDestination) toJSON() map[string]any {
	names := make([]string, 0, len(d.Headers))
	for name := range d.Headers {
		names = append(names, name)
	}
	sort.Strings(names)

	state := map[string]any{"type": "enabled"}
	if d.Disabled {
		state = map[string]any{"type": "disabled", "reason": map[string]any{"type": d.DisabledReason}}
	}

	return map[string]any{
		"id":           d.ID,
		"type":         "telemetry_destination",
		"created_at":   d.CreatedAt,
		"updated_at":   d.UpdatedAt,
		"display_name": d.DisplayName,
		"endpoint_url": d.EndpointURL,
		"header_names": names,
		"state":        state,
		"traces": map[string]any{
			"content_mode":      d.ContentMode,
			"enabled":           d.TracesEnabled,
			"endpoint_url":      d.TracesEndpoint,
			"max_content_bytes": d.MaxContentBytes,
		},
		"workspaces": map[string]any{"type": d.WorkspacesType},
		"delivery_health": map[string]any{
			"last_error":      d.LastError,
			"last_error_at":   d.LastErrorAt,
			"last_success_at": d.LastSuccessAt,
		},
	}
}

func (d *fakeTelemetryDestination) covers(workspaceID string) bool {
	switch d.WorkspacesType {
	case "include":
		return slices.Contains(d.WorkspaceIDs, workspaceID)
	case "exclude":
		return !slices.Contains(d.WorkspaceIDs, workspaceID)
	}
	return true
}

func (f *fakeAdminAPI) fakeTelemetryTime() string {
	f.telemetryClock++
	return fakeCreatedAt.Add(time.Duration(f.telemetryClock) * time.Second).Format(time.RFC3339Nano)
}

// registerTelemetryDestinationRoutes adds the telemetry destination and
// destination workspace endpoints to the fake's mux, all behind the beta
// header the real API requires. Called once from newFakeAdminAPI.
func (f *fakeAdminAPI) registerTelemetryDestinationRoutes() {
	f.telemetryDestinations = make(map[string]*fakeTelemetryDestination)

	f.mux.HandleFunc("POST /v1/organizations/telemetry_destinations", f.withTelemetryBeta(f.createTelemetryDestination))
	f.mux.HandleFunc("GET /v1/organizations/telemetry_destinations", f.withTelemetryBeta(f.listTelemetryDestinations))
	f.mux.HandleFunc("GET /v1/organizations/telemetry_destinations/{id}", f.withTelemetryBeta(f.withTelemetryDestination(f.getTelemetryDestination)))
	f.mux.HandleFunc("POST /v1/organizations/telemetry_destinations/{id}", f.withTelemetryBeta(f.withTelemetryDestination(f.updateTelemetryDestination)))
	f.mux.HandleFunc("DELETE /v1/organizations/telemetry_destinations/{id}", f.withTelemetryBeta(f.withTelemetryDestination(f.deleteTelemetryDestination)))
	f.mux.HandleFunc("GET /v1/organizations/telemetry_destinations/{id}/workspaces", f.withTelemetryBeta(f.withTelemetryDestination(f.listTelemetryDestinationWorkspaces)))
	f.mux.HandleFunc("POST /v1/organizations/telemetry_destinations/{id}/workspaces/{workspace_id}", f.withTelemetryBeta(f.withTelemetryDestination(f.addTelemetryDestinationWorkspace)))
	f.mux.HandleFunc("DELETE /v1/organizations/telemetry_destinations/{id}/workspaces/{workspace_id}", f.withTelemetryBeta(f.withTelemetryDestination(f.removeTelemetryDestinationWorkspace)))
}

func (f *fakeAdminAPI) withTelemetryBeta(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(strings.Split(r.Header.Get("anthropic-beta"), ","), fakeTelemetryBeta) {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "anthropic-beta: "+fakeTelemetryBeta+" is required")
			return
		}
		fn(w, r)
	}
}

func (f *fakeAdminAPI) withTelemetryDestination(fn func(http.ResponseWriter, *http.Request, *fakeTelemetryDestination)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dest, ok := f.telemetryDestinations[r.PathValue("id")]
		if !ok {
			writeAPIError(w, http.StatusNotFound, "not_found_error", "Telemetry destination not found")
			return
		}
		fn(w, r, dest)
	}
}

// fakeTelemetryRequest keeps every field as raw JSON so the fake can tell an
// omitted field from an explicit null, which the API treats differently.
type fakeTelemetryRequest struct {
	DisplayName json.RawMessage `json:"display_name"`
	EndpointURL json.RawMessage `json:"endpoint_url"`
	Headers     json.RawMessage `json:"headers"`
	State       json.RawMessage `json:"state"`
	Traces      json.RawMessage `json:"traces"`
	Workspaces  json.RawMessage `json:"workspaces"`
}

type fakeTelemetryTracesRequest struct {
	EndpointURL     json.RawMessage `json:"endpoint_url"`
	MaxContentBytes json.RawMessage `json:"max_content_bytes"`
	Enabled         json.RawMessage `json:"enabled"`
	ContentMode     json.RawMessage `json:"content_mode"`
}

type fakeTelemetryWorkspacesRequest struct {
	Type         string   `json:"type"`
	WorkspaceIDs []string `json:"workspace_ids"`
	hasIDs       bool
}

func present(raw json.RawMessage) bool { return raw != nil }

func isJSONNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func decodeFakeJSON(w http.ResponseWriter, raw []byte, into any) bool {
	if err := json.Unmarshal(raw, into); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return false
	}
	return true
}

func fakeInvalid(w http.ResponseWriter, format string, args ...any) bool {
	writeAPIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf(format, args...))
	return false
}

func validateFakeEndpoint(w http.ResponseWriter, field, raw string, allowPath bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return fakeInvalid(w, "%s: must be an https URL with a hostname", field)
	}
	if port := u.Port(); port != "" && port != "443" {
		return fakeInvalid(w, "%s: port must be 443", field)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return fakeInvalid(w, "%s: must not contain a query string, fragment or credentials", field)
	}
	if len(raw) > 2048 {
		return fakeInvalid(w, "%s: at most 2048 characters", field)
	}
	if !allowPath && strings.HasSuffix(strings.TrimSuffix(u.Path, "/"), "/v1/traces") {
		return fakeInvalid(w, "%s: must not include the per-signal path", field)
	}
	return true
}

func fakeAuthority(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return u.Scheme + "://" + strings.ToLower(u.Hostname()) + ":" + port
}

func validateFakeHeaderName(w http.ResponseWriter, name string) bool {
	lower := strings.ReplaceAll(strings.ToLower(name), "_", "-")
	if name == "" || len(name) > 128 {
		return fakeInvalid(w, "headers: names must be 1 to 128 characters")
	}
	if slices.Contains(fakeTelemetryReservedHeaders, lower) {
		return fakeInvalid(w, "headers: %q is a reserved name", name)
	}
	for _, prefix := range fakeTelemetryReservedHeaderPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return fakeInvalid(w, "headers: %q is a reserved name", name)
		}
	}
	return true
}

func validateFakeHeaderValue(w http.ResponseWriter, name, value string) bool {
	if len(value) > 4096 || value != strings.TrimSpace(value) {
		return fakeInvalid(w, "headers.%s: invalid value", name)
	}
	return true
}

func fakeHeaderBytes(headers map[string]string, unset map[string]bool) int {
	total := 0
	for name, value := range headers {
		total += len(name)
		if unset[name] {
			total += fakeTelemetryUnsetHeaderCost
		} else {
			total += len(value)
		}
	}
	return total
}

func (f *fakeAdminAPI) validateFakeWorkspaceIDs(w http.ResponseWriter, ids []string, alreadyOnList []string) bool {
	for _, id := range ids {
		ws, ok := f.workspaces[id]
		if !ok {
			return fakeInvalid(w, "workspaces.workspace_ids: %s is not a workspace of the organization", id)
		}
		if ws.ArchivedAt != nil && !slices.Contains(alreadyOnList, id) {
			return fakeInvalid(w, "workspaces.workspace_ids: %s is archived", id)
		}
	}
	return true
}

func parseFakeWorkspaces(w http.ResponseWriter, raw json.RawMessage) (fakeTelemetryWorkspacesRequest, bool) {
	var req fakeTelemetryWorkspacesRequest
	if !decodeFakeJSON(w, raw, &req) {
		return req, false
	}
	var probe map[string]json.RawMessage
	_ = json.Unmarshal(raw, &probe)
	_, req.hasIDs = probe["workspace_ids"]
	switch req.Type {
	case "all":
		if req.hasIDs {
			return req, fakeInvalid(w, "workspaces.workspace_ids: not permitted for type all")
		}
	case "include", "exclude":
	default:
		return req, fakeInvalid(w, "workspaces.type: must be all, include or exclude")
	}
	if len(req.WorkspaceIDs) > fakeTelemetryBodyIDLimit {
		return req, fakeInvalid(w, "workspaces.workspace_ids: at most %d per request", fakeTelemetryBodyIDLimit)
	}
	return req, true
}

func dedupe(ids []string) []string {
	out := []string{}
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// reverse returns the IDs newest first, matching how the API lists a
// destination's workspaces.
func reverse(ids []string) []string {
	out := slices.Clone(ids)
	slices.Reverse(out)
	return out
}

func (f *fakeAdminAPI) createTelemetryDestination(w http.ResponseWriter, r *http.Request) {
	var body fakeTelemetryRequest
	if !decodeFakeJSON(w, mustReadBody(r), &body) {
		return
	}
	var endpoint string
	if !present(body.EndpointURL) || !decodeFakeJSON(w, body.EndpointURL, &endpoint) || endpoint == "" {
		fakeInvalid(w, "endpoint_url: Field required")
		return
	}
	if !validateFakeEndpoint(w, "endpoint_url", endpoint, true) {
		return
	}
	if !present(body.Workspaces) {
		fakeInvalid(w, "workspaces: Field required")
		return
	}
	if len(f.telemetryDestinations) >= fakeTelemetryMaxDestinations {
		fakeInvalid(w, "An organization can have at most %d telemetry destinations", fakeTelemetryMaxDestinations)
		return
	}

	f.nextTelemetryDestinationID++
	dest := &fakeTelemetryDestination{
		ID:             fmt.Sprintf("tdst_%04d", f.nextTelemetryDestinationID),
		EndpointURL:    endpoint,
		Headers:        map[string]string{},
		ContentMode:    "metadata_only",
		TracesEnabled:  true,
		WorkspaceIDs:   []string{},
		CreatedAt:      f.fakeTelemetryTime(),
		sequence:       f.nextTelemetryDestinationID,
		WorkspacesType: "all",
	}
	dest.UpdatedAt = dest.CreatedAt

	if present(body.DisplayName) && !isJSONNull(body.DisplayName) {
		var name string
		if !decodeFakeJSON(w, body.DisplayName, &name) {
			return
		}
		if name == "" || len(name) > 255 {
			fakeInvalid(w, "display_name: 1 to 255 characters")
			return
		}
		dest.DisplayName = &name
	}

	if present(body.Headers) {
		var headers map[string]string
		if !decodeFakeJSON(w, body.Headers, &headers) {
			return
		}
		if !f.applyFakeHeaders(w, dest, headers, nil, false) {
			return
		}
	}

	if present(body.Traces) && !isJSONNull(body.Traces) {
		if !applyFakeTraces(w, dest, body.Traces) {
			return
		}
	}

	req, ok := parseFakeWorkspaces(w, body.Workspaces)
	if !ok {
		return
	}
	req.WorkspaceIDs = dedupe(req.WorkspaceIDs)
	if !f.validateFakeWorkspaceIDs(w, req.WorkspaceIDs, nil) {
		return
	}
	dest.WorkspacesType = req.Type
	dest.WorkspaceIDs = reverse(req.WorkspaceIDs)

	f.telemetryDestinations[dest.ID] = dest
	writeFakeJSON(w, http.StatusOK, dest.toJSON())
}

func mustReadBody(r *http.Request) []byte {
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(r.Body)
	return buf.Bytes()
}

// applyFakeHeaders merges headers into the destination by name. A nil value
// removes a header. Names compare case-insensitively.
func (f *fakeAdminAPI) applyFakeHeaders(w http.ResponseWriter, dest *fakeTelemetryDestination, headers map[string]string, removals map[string]bool, merge bool) bool {
	next := map[string]string{}
	base := dest.Headers
	if !merge {
		base = nil
	}
	for name, value := range base {
		next[name] = value
	}
	unset := map[string]bool{}
	for name := range next {
		unset[name] = merge
	}

	find := func(name string) (string, bool) {
		for existing := range next {
			if strings.EqualFold(existing, name) {
				return existing, true
			}
		}
		return "", false
	}

	for name := range removals {
		if existing, ok := find(name); ok {
			delete(next, existing)
			delete(unset, existing)
		}
	}
	seen := map[string]bool{}
	for name, value := range headers {
		if !validateFakeHeaderName(w, name) || !validateFakeHeaderValue(w, name, value) {
			return false
		}
		lower := strings.ToLower(name)
		if seen[lower] {
			return fakeInvalid(w, "headers: names cannot differ only in case")
		}
		seen[lower] = true
		if existing, ok := find(name); ok {
			delete(next, existing)
			delete(unset, existing)
		}
		next[name] = value
		unset[name] = false
	}

	if len(next) > fakeTelemetryMaxHeaders {
		return fakeInvalid(w, "headers: at most %d headers", fakeTelemetryMaxHeaders)
	}
	if fakeHeaderBytes(next, unset) > fakeTelemetryMaxHeaderBytes {
		return fakeInvalid(w, "headers: names and values together are at most %d bytes", fakeTelemetryMaxHeaderBytes)
	}
	dest.Headers = next
	return true
}

func applyFakeTraces(w http.ResponseWriter, dest *fakeTelemetryDestination, raw json.RawMessage) bool {
	var req fakeTelemetryTracesRequest
	if !decodeFakeJSON(w, raw, &req) {
		return false
	}
	if present(req.EndpointURL) {
		if isJSONNull(req.EndpointURL) {
			dest.TracesEndpoint = nil
		} else {
			var endpoint string
			if !decodeFakeJSON(w, req.EndpointURL, &endpoint) || !validateFakeEndpoint(w, "traces.endpoint_url", endpoint, true) {
				return false
			}
			if fakeAuthority(endpoint) != fakeAuthority(dest.EndpointURL) {
				return fakeInvalid(w, "traces.endpoint_url: must have the same scheme, host and port as endpoint_url")
			}
			dest.TracesEndpoint = &endpoint
		}
	}
	if present(req.MaxContentBytes) {
		if isJSONNull(req.MaxContentBytes) {
			dest.MaxContentBytes = nil
		} else {
			var n int64
			if !decodeFakeJSON(w, req.MaxContentBytes, &n) {
				return false
			}
			if n < 1 {
				return fakeInvalid(w, "traces.max_content_bytes: must be at least 1")
			}
			dest.MaxContentBytes = &n
		}
	}
	if present(req.Enabled) {
		if isJSONNull(req.Enabled) {
			return fakeInvalid(w, "traces.enabled: cannot be null")
		}
		if !decodeFakeJSON(w, req.Enabled, &dest.TracesEnabled) {
			return false
		}
	}
	if present(req.ContentMode) {
		if isJSONNull(req.ContentMode) {
			dest.ContentMode = "metadata_only"
		} else {
			var mode string
			if !decodeFakeJSON(w, req.ContentMode, &mode) {
				return false
			}
			if mode != "metadata_only" && mode != "content" {
				return fakeInvalid(w, "traces.content_mode: must be metadata_only or content")
			}
			dest.ContentMode = mode
		}
	}
	return true
}

func (f *fakeAdminAPI) getTelemetryDestination(w http.ResponseWriter, _ *http.Request, dest *fakeTelemetryDestination) {
	writeFakeJSON(w, http.StatusOK, dest.toJSON())
}

func (f *fakeAdminAPI) deleteTelemetryDestination(w http.ResponseWriter, _ *http.Request, dest *fakeTelemetryDestination) {
	delete(f.telemetryDestinations, dest.ID)
	writeFakeJSON(w, http.StatusOK, map[string]any{"id": dest.ID, "type": "telemetry_destination_deleted"})
}

func (f *fakeAdminAPI) updateTelemetryDestination(w http.ResponseWriter, r *http.Request, dest *fakeTelemetryDestination) {
	var body fakeTelemetryRequest
	raw := mustReadBody(r)
	f.telemetryUpdateBodies = append(f.telemetryUpdateBodies, string(raw))
	if !decodeFakeJSON(w, raw, &body) {
		return
	}
	if !present(body.DisplayName) && !present(body.EndpointURL) && !present(body.Headers) && !present(body.State) && !present(body.Traces) && !present(body.Workspaces) {
		fakeInvalid(w, "Send at least one field")
		return
	}
	if f.telemetryConflicts {
		writeAPIError(w, http.StatusConflict, "conflict_error", "The destination was changed by another request")
		return
	}

	updated := *dest
	updated.Headers = maps.Clone(dest.Headers)
	updated.WorkspaceIDs = slices.Clone(dest.WorkspaceIDs)

	if present(body.DisplayName) {
		if isJSONNull(body.DisplayName) {
			updated.DisplayName = nil
		} else {
			var name string
			if !decodeFakeJSON(w, body.DisplayName, &name) {
				return
			}
			if name == "" || len(name) > 255 {
				fakeInvalid(w, "display_name: 1 to 255 characters")
				return
			}
			updated.DisplayName = &name
		}
	}

	endpointSent := false
	if present(body.EndpointURL) {
		var endpoint string
		if isJSONNull(body.EndpointURL) || !decodeFakeJSON(w, body.EndpointURL, &endpoint) {
			fakeInvalid(w, "endpoint_url: cannot be null")
			return
		}
		if !validateFakeEndpoint(w, "endpoint_url", endpoint, true) {
			return
		}
		endpointSent = true
		if fakeAuthority(endpoint) != fakeAuthority(dest.EndpointURL) {
			updated.Headers = map[string]string{}
			if updated.TracesEndpoint != nil && fakeAuthority(*updated.TracesEndpoint) != fakeAuthority(endpoint) {
				tracesChangesIt := false
				if present(body.Traces) {
					var probe map[string]json.RawMessage
					_ = json.Unmarshal(body.Traces, &probe)
					_, tracesChangesIt = probe["endpoint_url"]
				}
				if !tracesChangesIt {
					fakeInvalid(w, "traces.endpoint_url: must be set to a URL with the new hostname and port, or to null")
					return
				}
			}
		}
		updated.EndpointURL = endpoint
	}

	if present(body.Headers) {
		if isJSONNull(body.Headers) {
			fakeInvalid(w, "headers: cannot be null")
			return
		}
		var raw map[string]*string
		if !decodeFakeJSON(w, body.Headers, &raw) {
			return
		}
		set := map[string]string{}
		removals := map[string]bool{}
		for name, value := range raw {
			if value == nil {
				removals[name] = true
			} else {
				set[name] = *value
			}
		}
		if len(set) > 0 && !endpointSent {
			fakeInvalid(w, "endpoint_url: required when headers sets a value")
			return
		}
		if !f.applyFakeHeaders(w, &updated, set, removals, true) {
			return
		}
	}

	if present(body.Traces) {
		if isJSONNull(body.Traces) {
			updated.ContentMode, updated.TracesEnabled, updated.TracesEndpoint, updated.MaxContentBytes = "metadata_only", true, nil, nil
		} else if !applyFakeTraces(w, &updated, body.Traces) {
			return
		}
	}

	if present(body.State) {
		var state struct {
			Type string `json:"type"`
		}
		if isJSONNull(body.State) || !decodeFakeJSON(w, body.State, &state) {
			fakeInvalid(w, "state: cannot be null")
			return
		}
		switch state.Type {
		case "enabled":
			updated.Disabled, updated.DisabledReason = false, ""
		case "disabled":
			if !updated.Disabled {
				updated.Disabled, updated.DisabledReason = true, "caller"
			}
		default:
			fakeInvalid(w, "state.type: must be enabled or disabled")
			return
		}
	}

	if present(body.Workspaces) {
		req, ok := parseFakeWorkspaces(w, body.Workspaces)
		if !ok {
			return
		}
		if len(dest.WorkspaceIDs) > fakeTelemetryReplaceLimit && (req.Type != dest.WorkspacesType || req.hasIDs) {
			fakeInvalid(w, "The list holds more than %d workspaces. Remove workspaces until %d or fewer remain", fakeTelemetryReplaceLimit, fakeTelemetryReplaceLimit)
			return
		}
		ids := dedupe(req.WorkspaceIDs)
		if !f.validateFakeWorkspaceIDs(w, ids, dest.WorkspaceIDs) {
			return
		}
		switch {
		case req.Type != dest.WorkspacesType:
			updated.WorkspaceIDs = reverse(ids)
		case req.hasIDs:
			kept := []string{}
			for _, id := range dest.WorkspaceIDs {
				if slices.Contains(ids, id) {
					kept = append(kept, id)
				}
			}
			for _, id := range ids {
				if !slices.Contains(kept, id) {
					kept = append([]string{id}, kept...)
				}
			}
			updated.WorkspaceIDs = kept
		}
		updated.WorkspacesType = req.Type
	}

	updated.UpdatedAt = f.fakeTelemetryTime()
	*dest = updated
	writeFakeJSON(w, http.StatusOK, dest.toJSON())
}

// fakePage slices a newest-first list into pages of fakeTelemetryPageSize,
// using the offset of the next page as the opaque cursor.
func fakePage[T any](w http.ResponseWriter, r *http.Request, items []T, encode func(T) any) {
	start := 0
	if cursor := r.URL.Query().Get("page"); cursor != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(cursor, "cursor_"))
		if err != nil {
			fakeInvalid(w, "page: invalid cursor")
			return
		}
		start = n
	}
	end := min(start+fakeTelemetryPageSize, len(items))
	data := make([]any, 0, end-start)
	for _, item := range items[min(start, len(items)):end] {
		data = append(data, encode(item))
	}
	var next any
	if end < len(items) {
		next = fmt.Sprintf("cursor_%d", end)
	}
	writeFakeJSON(w, http.StatusOK, map[string]any{"data": data, "next_page": next})
}

func (f *fakeAdminAPI) listTelemetryDestinations(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query()["workspace_ids[]"]
	if len(filter) > 100 {
		fakeInvalid(w, "workspace_ids: at most 100")
		return
	}

	all := make([]*fakeTelemetryDestination, 0, len(f.telemetryDestinations))
	for _, dest := range f.telemetryDestinations {
		all = append(all, dest)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].sequence > all[j].sequence })

	matched := []*fakeTelemetryDestination{}
	for _, dest := range all {
		if len(filter) > 0 {
			covers := false
			for _, id := range filter {
				if _, ok := f.workspaces[id]; ok && dest.covers(id) {
					covers = true
				}
			}
			if !covers {
				continue
			}
		}
		matched = append(matched, dest)
	}
	fakePage(w, r, matched, func(d *fakeTelemetryDestination) any { return d.toJSON() })
}

func (f *fakeAdminAPI) listTelemetryDestinationWorkspaces(w http.ResponseWriter, r *http.Request, dest *fakeTelemetryDestination) {
	ids := dest.WorkspaceIDs
	if dest.WorkspacesType == "all" {
		ids = nil
	}
	fakePage(w, r, ids, func(id string) any {
		return map[string]any{
			"type":                     "telemetry_destination_workspace",
			"telemetry_destination_id": dest.ID,
			"workspace_id":             id,
			"created_at":               dest.CreatedAt,
		}
	})
}

func (f *fakeAdminAPI) addTelemetryDestinationWorkspace(w http.ResponseWriter, r *http.Request, dest *fakeTelemetryDestination) {
	workspaceID := r.PathValue("workspace_id")
	if dest.WorkspacesType == "all" {
		fakeInvalid(w, "A destination whose workspaces.type is all does not have a list")
		return
	}
	if !slices.Contains(dest.WorkspaceIDs, workspaceID) {
		if len(dest.WorkspaceIDs) >= fakeTelemetryMaxList {
			fakeInvalid(w, "A list holds at most %d workspaces", fakeTelemetryMaxList)
			return
		}
		if !f.validateFakeWorkspaceIDs(w, []string{workspaceID}, nil) {
			return
		}
		dest.WorkspaceIDs = append([]string{workspaceID}, dest.WorkspaceIDs...)
	}
	writeFakeJSON(w, http.StatusOK, map[string]any{
		"type":                     "telemetry_destination_workspace",
		"telemetry_destination_id": dest.ID,
		"workspace_id":             workspaceID,
		"created_at":               dest.CreatedAt,
	})
}

func (f *fakeAdminAPI) removeTelemetryDestinationWorkspace(w http.ResponseWriter, r *http.Request, dest *fakeTelemetryDestination) {
	workspaceID := r.PathValue("workspace_id")
	if dest.WorkspacesType == "all" {
		fakeInvalid(w, "A destination whose workspaces.type is all does not have a list")
		return
	}
	index := slices.Index(dest.WorkspaceIDs, workspaceID)
	if index < 0 {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "Workspace is not on the destination's list")
		return
	}
	dest.WorkspaceIDs = slices.Delete(dest.WorkspaceIDs, index, index+1)
	writeFakeJSON(w, http.StatusOK, map[string]any{
		"type":                     "telemetry_destination_workspace_deleted",
		"telemetry_destination_id": dest.ID,
		"workspace_id":             workspaceID,
	})
}

// seedTelemetryDestination, disableTelemetryDestination,
// removeTelemetryDestination and removeTelemetryDestinationHeader simulate
// changes made outside Terraform. Callers outside ServeHTTP must hold mu.
type fakeTelemetryDestinationSeed struct {
	EndpointURL    string
	WorkspacesType string
	WorkspaceIDs   []string
	Headers        map[string]string
}

func (f *fakeAdminAPI) seedTelemetryDestination(seed fakeTelemetryDestinationSeed) string {
	f.nextTelemetryDestinationID++
	id := fmt.Sprintf("tdst_%04d", f.nextTelemetryDestinationID)
	endpoint := seed.EndpointURL
	if endpoint == "" {
		endpoint = "https://seeded.example.com"
	}
	kind := seed.WorkspacesType
	if kind == "" {
		kind = "all"
	}
	headers := seed.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	created := f.fakeTelemetryTime()
	f.telemetryDestinations[id] = &fakeTelemetryDestination{
		ID:             id,
		EndpointURL:    endpoint,
		Headers:        headers,
		ContentMode:    "metadata_only",
		TracesEnabled:  true,
		WorkspacesType: kind,
		WorkspaceIDs:   reverse(seed.WorkspaceIDs),
		CreatedAt:      created,
		UpdatedAt:      created,
		sequence:       f.nextTelemetryDestinationID,
	}
	return id
}

func (f *fakeAdminAPI) disableTelemetryDestination(id, reason string) {
	dest := f.telemetryDestinations[id]
	dest.Disabled, dest.DisabledReason = true, reason
}

func (f *fakeAdminAPI) removeTelemetryDestination(id string) {
	delete(f.telemetryDestinations, id)
}

func (f *fakeAdminAPI) removeTelemetryDestinationHeader(id, name string) {
	delete(f.telemetryDestinations[id].Headers, name)
}
