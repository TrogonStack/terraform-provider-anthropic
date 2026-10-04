package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// fakeAPIKeyPageSize caps every list response at 2 items regardless of the
// requested limit, so tests exercise ListAutoPaging's has_more/last_id
// cursor instead of always getting everything back in one page.
const fakeAPIKeyPageSize = 2

type fakeAPIKeyActor struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type fakeAPIKeyPrincipal struct {
	Type             string `json:"type"`
	UserID           string `json:"user_id,omitempty"`
	ServiceAccountID string `json:"service_account_id,omitempty"`
}

type fakeAPIKeyScope struct {
	Type        string `json:"type"`
	WorkspaceID string `json:"workspace_id,omitempty"`
}

type fakeAPIKey struct {
	ID             string
	Name           string
	Status         string
	CreatedAt      string
	ExpiresAt      *string
	PartialKeyHint string
	CreatedBy      *fakeAPIKeyActor
	Principal      *fakeAPIKeyPrincipal
	Scope          fakeAPIKeyScope
}

func (k *fakeAPIKey) toJSON() map[string]any {
	return map[string]any{
		"id":               k.ID,
		"type":             "api_key",
		"name":             k.Name,
		"status":           k.Status,
		"created_at":       k.CreatedAt,
		"expires_at":       fakeTimePtrJSON(k.ExpiresAt),
		"partial_key_hint": k.PartialKeyHint,
		"created_by":       k.CreatedBy,
		"principal":        k.Principal,
		"scope":            k.Scope,
	}
}

func fakeTimePtrJSON(t *string) any {
	if t == nil {
		return nil
	}
	return *t
}

// fakeAPIKeySeed describes a key to plant in the fake outside Terraform.
// An empty WorkspaceID seeds an organization-scoped key, an empty
// PrincipalType seeds a principal-less key, an empty CreatedByID seeds a
// key with no recorded creator, and an empty ExpiresAt seeds a key that
// never expires.
type fakeAPIKeySeed struct {
	Name           string
	Status         string
	WorkspaceID    string
	PrincipalType  string // "user" or "service_account"
	PrincipalID    string
	CreatedByID    string
	CreatedByType  string // "user" or "service_account"
	ExpiresAt      string
	PartialKeyHint string
}

// seedAPIKey plants a key as if it had been created in the Claude Console.
// Callers outside ServeHTTP must hold mu.
func (f *fakeAdminAPI) seedAPIKey(seed fakeAPIKeySeed) string {
	f.nextAPIKeyID++
	id := fmt.Sprintf("apikey_%04d", f.nextAPIKeyID)

	scope := fakeAPIKeyScope{Type: "organization"}
	if seed.WorkspaceID != "" {
		scope = fakeAPIKeyScope{Type: "workspace", WorkspaceID: seed.WorkspaceID}
	}

	var principal *fakeAPIKeyPrincipal
	switch seed.PrincipalType {
	case "user":
		principal = &fakeAPIKeyPrincipal{Type: "user_actor", UserID: seed.PrincipalID}
	case "service_account":
		principal = &fakeAPIKeyPrincipal{Type: "service_account_actor", ServiceAccountID: seed.PrincipalID}
	}

	var createdBy *fakeAPIKeyActor
	if seed.CreatedByID != "" {
		createdBy = &fakeAPIKeyActor{ID: seed.CreatedByID, Type: seed.CreatedByType}
	}

	var expiresAt *string
	if seed.ExpiresAt != "" {
		expiresAt = &seed.ExpiresAt
	}

	hint := seed.PartialKeyHint
	if hint == "" {
		hint = "sk-ant-api03-R2D...igAA"
	}

	status := seed.Status
	if status == "" {
		status = "active"
	}

	f.apiKeys[id] = &fakeAPIKey{
		ID:             id,
		Name:           seed.Name,
		Status:         status,
		CreatedAt:      fakeCreatedAt.Format(time.RFC3339Nano),
		ExpiresAt:      expiresAt,
		PartialKeyHint: hint,
		CreatedBy:      createdBy,
		Principal:      principal,
		Scope:          scope,
	}
	return id
}

func (f *fakeAdminAPI) registerAPIKeyRoutes() {
	f.mux.HandleFunc("GET /v1/organizations/api_keys", f.listAPIKeys)
	f.mux.HandleFunc("GET /v1/organizations/api_keys/{id}", f.withAPIKey(f.getAPIKey))
	f.mux.HandleFunc("POST /v1/organizations/api_keys/{id}", f.withAPIKey(f.updateAPIKey))
}

func (f *fakeAdminAPI) withAPIKey(fn func(http.ResponseWriter, *http.Request, *fakeAPIKey)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := f.apiKeys[r.PathValue("id")]
		if !ok {
			writeAPIError(w, http.StatusNotFound, "not_found_error", "API Key not found")
			return
		}
		fn(w, r, key)
	}
}

func (f *fakeAdminAPI) getAPIKey(w http.ResponseWriter, _ *http.Request, key *fakeAPIKey) {
	writeFakeJSON(w, http.StatusOK, key.toJSON())
}

type fakeAPIKeyUpdateRequest struct {
	Name   *string `json:"name"`
	Status *string `json:"status"`
}

func (f *fakeAdminAPI) updateAPIKey(w http.ResponseWriter, r *http.Request, key *fakeAPIKey) {
	var body fakeAPIKeyUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if body.Status != nil && *body.Status == "expired" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "status: Input should be 'active', 'archived' or 'inactive'")
		return
	}

	if body.Name != nil {
		key.Name = *body.Name
	}
	if body.Status != nil {
		key.Status = *body.Status
	}
	writeFakeJSON(w, http.StatusOK, key.toJSON())
}

func (f *fakeAdminAPI) listAPIKeys(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	workspaceID := q.Get("workspace_id")
	status := q.Get("status")
	createdByUserID := q.Get("created_by_user_id")
	afterID := q.Get("after_id")

	ids := make([]string, 0, len(f.apiKeys))
	for id := range f.apiKeys {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var filtered []*fakeAPIKey
	for _, id := range ids {
		key := f.apiKeys[id]
		if workspaceID != "" && key.Scope.WorkspaceID != workspaceID {
			continue
		}
		if status != "" && key.Status != status {
			continue
		}
		if createdByUserID != "" && (key.CreatedBy == nil || key.CreatedBy.ID != createdByUserID) {
			continue
		}
		filtered = append(filtered, key)
	}

	start := 0
	if afterID != "" {
		for i, key := range filtered {
			if key.ID == afterID {
				start = i + 1
				break
			}
		}
	}
	end := min(start+fakeAPIKeyPageSize, len(filtered))
	page := filtered[start:end]

	data := make([]map[string]any, 0, len(page))
	for _, key := range page {
		data = append(data, key.toJSON())
	}

	body := map[string]any{
		"data":     data,
		"has_more": end < len(filtered),
		"first_id": nil,
		"last_id":  nil,
	}
	if len(page) > 0 {
		body["first_id"] = page[0].ID
		body["last_id"] = page[len(page)-1].ID
	}
	writeFakeJSON(w, http.StatusOK, body)
}
