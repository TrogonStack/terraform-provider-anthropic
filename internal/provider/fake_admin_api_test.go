package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"
)

const testAPIKey = "sk-ant-admin-test"

var fakeCreatedAt = time.Date(2024, 10, 30, 23, 58, 27, 0, time.UTC)

var fakeDisplayColors = []string{"#6C5BB9", "#D97757", "#4A90A4"}

type fakeDataResidency struct {
	WorkspaceGeo         string `json:"workspace_geo"`
	AllowedInferenceGeos any    `json:"allowed_inference_geos"`
	DefaultInferenceGeo  string `json:"default_inference_geo"`
}

type fakeWorkspace struct {
	ID            string            `json:"id"`
	Type          string            `json:"type"`
	Name          string            `json:"name"`
	DisplayColor  string            `json:"display_color"`
	Tags          map[string]string `json:"tags"`
	ExternalKeyID *string           `json:"external_key_id"`
	CompartmentID string            `json:"compartment_id"`
	CreatedAt     string            `json:"created_at"`
	ArchivedAt    *string           `json:"archived_at"`
	DataResidency fakeDataResidency `json:"data_residency"`
}

type fakeWorkspaceRequest struct {
	Name          *string            `json:"name"`
	DisplayColor  *string            `json:"display_color"`
	Tags          *map[string]string `json:"tags"`
	ExternalKeyID *string            `json:"external_key_id"`
	DataResidency *struct {
		WorkspaceGeo         *string         `json:"workspace_geo"`
		AllowedInferenceGeos json.RawMessage `json:"allowed_inference_geos"`
		DefaultInferenceGeo  *string         `json:"default_inference_geo"`
	} `json:"data_residency"`
}

// fakeAdminAPI is an in-memory stand-in for the Admin API workspace endpoints.
// It replaces tags wholesale on update, matching how the provider sends them.
type fakeAdminAPI struct {
	mu                   sync.Mutex
	nextID               int
	workspaces           map[string]*fakeWorkspace
	defaultWorkspaceID   string
	nextServiceAccountID int
	serviceAccounts      map[string]*fakeServiceAccount
	workspaceMembers     map[string]*fakeServiceAccountMembership
	nextAPIKeyID         int
	apiKeys              map[string]*fakeAPIKey
	mux                  *http.ServeMux
}

func newFakeAdminAPI() *fakeAdminAPI {
	f := &fakeAdminAPI{
		workspaces: make(map[string]*fakeWorkspace),
		apiKeys:    make(map[string]*fakeAPIKey),
	}
	f.mux = http.NewServeMux()
	f.mux.HandleFunc("POST /v1/organizations/workspaces", f.createWorkspace)
	f.mux.HandleFunc("GET /v1/organizations/workspaces/{id}", f.withWorkspace(f.getWorkspace))
	f.mux.HandleFunc("POST /v1/organizations/workspaces/{id}", f.withWorkspace(f.updateWorkspace))
	f.mux.HandleFunc("POST /v1/organizations/workspaces/{id}/archive", f.withWorkspace(f.archiveWorkspace))
	f.registerServiceAccountRoutes()
	f.registerAPIKeyRoutes()
	f.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, http.StatusNotFound, "not_found_error", fmt.Sprintf("%s %s is not served by the fake", r.Method, r.URL.Path))
	})
	return f
}

func (f *fakeAdminAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Api-Key") != testAPIKey {
		writeAPIError(w, http.StatusUnauthorized, "authentication_error", "invalid x-api-key")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mux.ServeHTTP(w, r)
}

func (f *fakeAdminAPI) withWorkspace(fn func(http.ResponseWriter, *http.Request, *fakeWorkspace)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ws, ok := f.workspaces[r.PathValue("id")]
		if !ok {
			writeAPIError(w, http.StatusNotFound, "not_found_error", "Workspace not found")
			return
		}
		fn(w, r, ws)
	}
}

func (f *fakeAdminAPI) createWorkspace(w http.ResponseWriter, r *http.Request) {
	var body fakeWorkspaceRequest
	if !decodeFakeRequest(w, r, &body) {
		return
	}
	if body.Name == nil || *body.Name == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "name: Field required")
		return
	}

	ws := f.newWorkspace(*body.Name)
	if body.DisplayColor != nil {
		ws.DisplayColor = *body.DisplayColor
	}
	if body.Tags != nil {
		ws.Tags = *body.Tags
	}
	ws.ExternalKeyID = body.ExternalKeyID
	if body.DataResidency != nil {
		if body.DataResidency.WorkspaceGeo != nil {
			ws.DataResidency.WorkspaceGeo = *body.DataResidency.WorkspaceGeo
		}
		if !applyFakeResidency(w, ws, body) {
			return
		}
	}
	f.workspaces[ws.ID] = ws
	writeFakeJSON(w, http.StatusOK, ws)
}

func (f *fakeAdminAPI) newWorkspace(name string) *fakeWorkspace {
	f.nextID++
	return &fakeWorkspace{
		ID:            fmt.Sprintf("wrkspc_%04d", f.nextID),
		Type:          "workspace",
		Name:          name,
		DisplayColor:  fakeDisplayColors[f.nextID%len(fakeDisplayColors)],
		Tags:          map[string]string{},
		CompartmentID: fmt.Sprintf("compartment-%04d", f.nextID),
		CreatedAt:     fakeCreatedAt.Format(time.RFC3339Nano),
		DataResidency: fakeDataResidency{WorkspaceGeo: "us", AllowedInferenceGeos: "unrestricted", DefaultInferenceGeo: "global"},
	}
}

func (f *fakeAdminAPI) getWorkspace(w http.ResponseWriter, _ *http.Request, ws *fakeWorkspace) {
	writeFakeJSON(w, http.StatusOK, ws)
}

func (f *fakeAdminAPI) updateWorkspace(w http.ResponseWriter, r *http.Request, ws *fakeWorkspace) {
	var body fakeWorkspaceRequest
	if !decodeFakeRequest(w, r, &body) {
		return
	}
	if ws.ArchivedAt != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "Cannot update an archived workspace")
		return
	}
	if body.ExternalKeyID != nil && ws.ExternalKeyID != nil && *body.ExternalKeyID != *ws.ExternalKeyID {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "external_key_id cannot be changed once set")
		return
	}
	if body.DataResidency != nil && body.DataResidency.WorkspaceGeo != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "data_residency.workspace_geo: Extra inputs are not permitted")
		return
	}

	updated := *ws
	if body.Name != nil {
		updated.Name = *body.Name
	}
	if body.DisplayColor != nil {
		updated.DisplayColor = *body.DisplayColor
	}
	if body.Tags != nil {
		updated.Tags = *body.Tags
	}
	if body.ExternalKeyID != nil {
		updated.ExternalKeyID = body.ExternalKeyID
	}
	if body.DataResidency != nil && !applyFakeResidency(w, &updated, body) {
		return
	}
	*ws = updated
	writeFakeJSON(w, http.StatusOK, ws)
}

func (f *fakeAdminAPI) archiveWorkspace(w http.ResponseWriter, _ *http.Request, ws *fakeWorkspace) {
	if ws.ArchivedAt != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "Workspace is already archived")
		return
	}
	f.archive(ws.ID)
	writeFakeJSON(w, http.StatusOK, ws)
}

// seed, archive and remove simulate changes made outside Terraform. Callers
// outside ServeHTTP must hold mu.
func (f *fakeAdminAPI) seed(name string, residency fakeDataResidency) string {
	ws := f.newWorkspace(name)
	ws.DataResidency = residency
	f.workspaces[ws.ID] = ws
	return ws.ID
}

func (f *fakeAdminAPI) seedDefaultWorkspace(name string) string {
	id := f.seed(name, fakeDataResidency{WorkspaceGeo: "us", AllowedInferenceGeos: "unrestricted", DefaultInferenceGeo: "global"})
	f.defaultWorkspaceID = id
	return id
}

func (f *fakeAdminAPI) archive(id string) {
	archivedAt := fakeCreatedAt.Add(time.Hour).Format(time.RFC3339Nano)
	f.workspaces[id].ArchivedAt = &archivedAt
}

func (f *fakeAdminAPI) remove(id string) {
	delete(f.workspaces, id)
}

func applyFakeResidency(w http.ResponseWriter, ws *fakeWorkspace, body fakeWorkspaceRequest) bool {
	residency := body.DataResidency
	if len(residency.AllowedInferenceGeos) > 0 {
		var geos []string
		if json.Unmarshal(residency.AllowedInferenceGeos, &geos) == nil {
			ws.DataResidency.AllowedInferenceGeos = geos
		} else {
			ws.DataResidency.AllowedInferenceGeos = "unrestricted"
		}
	}
	if residency.DefaultInferenceGeo != nil {
		ws.DataResidency.DefaultInferenceGeo = *residency.DefaultInferenceGeo
	}
	if geos, ok := ws.DataResidency.AllowedInferenceGeos.([]string); ok && !slices.Contains(geos, ws.DataResidency.DefaultInferenceGeo) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "default_inference_geo must be a member of allowed_inference_geos")
		return false
	}
	return true
}

func decodeFakeRequest(w http.ResponseWriter, r *http.Request, body *fakeWorkspaceRequest) bool {
	if err := json.NewDecoder(r.Body).Decode(body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return false
	}
	return true
}

func writeAPIError(w http.ResponseWriter, status int, errorType, message string) {
	writeFakeJSON(w, status, map[string]any{
		"type":  "error",
		"error": map[string]any{"type": errorType, "message": message},
	})
}

func writeFakeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
