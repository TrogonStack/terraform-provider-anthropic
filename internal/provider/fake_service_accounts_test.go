package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"
)

const fakeActorID = "user_fake0001"

var fakeServiceAccountNamePattern = regexp.MustCompile(`^[a-z0-9-]+$`)

type fakeServiceAccount struct {
	ID                string  `json:"id"`
	Type              string  `json:"type"`
	Name              string  `json:"name"`
	Description       string  `json:"description"`
	OrganizationRole  string  `json:"organization_role"`
	CreatedAt         string  `json:"created_at"`
	CreatedByActorID  string  `json:"created_by_actor_id"`
	UpdatedAt         string  `json:"updated_at"`
	UpdatedByActorID  string  `json:"updated_by_actor_id"`
	ArchivedAt        *string `json:"archived_at"`
	ArchivedByActorID string  `json:"archived_by_actor_id"`
	// HasLiveRule simulates a federation rule still targeting this service
	// account. The fake does not model federation rules themselves, only the
	// archive rejection they cause; set this field directly from a test.
	HasLiveRule bool `json:"-"`
}

type fakeServiceAccountMembership struct {
	ServiceAccountID string `json:"service_account_id"`
	WorkspaceID      string `json:"workspace_id"`
	WorkspaceRole    string `json:"workspace_role"`
	Implicit         bool   `json:"implicit"`
	CreatedByActorID string `json:"created_by_actor_id"`
	Type             string `json:"type"`
}

// registerServiceAccountRoutes adds the service account and workspace
// membership endpoints to the fake's mux. Called once from newFakeAdminAPI.
func (f *fakeAdminAPI) registerServiceAccountRoutes() {
	f.serviceAccounts = make(map[string]*fakeServiceAccount)
	f.workspaceMembers = make(map[string]*fakeServiceAccountMembership)

	f.mux.HandleFunc("POST /v1/organizations/service_accounts", f.createServiceAccount)
	f.mux.HandleFunc("GET /v1/organizations/service_accounts/{id}", f.withServiceAccount(f.getServiceAccount))
	f.mux.HandleFunc("POST /v1/organizations/service_accounts/{id}", f.withServiceAccount(f.updateServiceAccount))
	f.mux.HandleFunc("POST /v1/organizations/service_accounts/{id}/archive", f.withServiceAccount(f.archiveServiceAccountRoute))

	f.mux.HandleFunc("POST /v1/organizations/workspaces/{workspace_id}/service_accounts", f.addWorkspaceServiceAccount)
	f.mux.HandleFunc("GET /v1/organizations/workspaces/{workspace_id}/service_accounts/{service_account_id}", f.getWorkspaceServiceAccount)
	f.mux.HandleFunc("POST /v1/organizations/workspaces/{workspace_id}/service_accounts/{service_account_id}", f.updateWorkspaceServiceAccount)
	f.mux.HandleFunc("DELETE /v1/organizations/workspaces/{workspace_id}/service_accounts/{service_account_id}", f.removeWorkspaceServiceAccount)
}

func (f *fakeAdminAPI) withServiceAccount(fn func(http.ResponseWriter, *http.Request, *fakeServiceAccount)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sa, ok := f.serviceAccounts[r.PathValue("id")]
		if !ok {
			writeAPIError(w, http.StatusNotFound, "not_found_error", "Service account not found")
			return
		}
		fn(w, r, sa)
	}
}

func (f *fakeAdminAPI) newServiceAccount(name string) *fakeServiceAccount {
	f.nextServiceAccountID++
	return &fakeServiceAccount{
		ID:               fmt.Sprintf("svac_%04d", f.nextServiceAccountID),
		Type:             "service_account",
		Name:             name,
		OrganizationRole: "developer",
		CreatedAt:        fakeCreatedAt.Format(time.RFC3339Nano),
		CreatedByActorID: fakeActorID,
		UpdatedAt:        fakeCreatedAt.Format(time.RFC3339Nano),
		UpdatedByActorID: fakeActorID,
	}
}

func (f *fakeAdminAPI) createServiceAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name             *string `json:"name"`
		Description      *string `json:"description"`
		OrganizationRole *string `json:"organization_role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if body.Name == nil || *body.Name == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "name: Field required")
		return
	}
	if !fakeServiceAccountNamePattern.MatchString(*body.Name) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "name: must match ^[a-z0-9-]+$")
		return
	}
	for _, sa := range f.serviceAccounts {
		if sa.Name == *body.Name && sa.ArchivedAt == nil {
			writeAPIError(w, http.StatusConflict, "invalid_request_error", fmt.Sprintf("A service account named %q already exists", *body.Name))
			return
		}
	}

	sa := f.newServiceAccount(*body.Name)
	if body.Description != nil {
		sa.Description = *body.Description
	}
	if body.OrganizationRole != nil {
		sa.OrganizationRole = *body.OrganizationRole
	}
	f.serviceAccounts[sa.ID] = sa
	writeFakeJSON(w, http.StatusOK, sa)
}

func (f *fakeAdminAPI) getServiceAccount(w http.ResponseWriter, _ *http.Request, sa *fakeServiceAccount) {
	writeFakeJSON(w, http.StatusOK, sa)
}

func (f *fakeAdminAPI) updateServiceAccount(w http.ResponseWriter, r *http.Request, sa *fakeServiceAccount) {
	if sa.ArchivedAt != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "Cannot update an archived service account")
		return
	}

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	if v, ok := raw["description"]; ok {
		if string(v) == "null" {
			sa.Description = ""
		} else {
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
				return
			}
			sa.Description = s
		}
	}
	if v, ok := raw["organization_role"]; ok && string(v) != "null" {
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		sa.OrganizationRole = s
	}

	sa.UpdatedAt = fakeCreatedAt.Add(time.Hour).Format(time.RFC3339Nano)
	writeFakeJSON(w, http.StatusOK, sa)
}

func (f *fakeAdminAPI) archiveServiceAccountRoute(w http.ResponseWriter, _ *http.Request, sa *fakeServiceAccount) {
	if sa.ArchivedAt != nil {
		writeFakeJSON(w, http.StatusOK, sa)
		return
	}
	if sa.HasLiveRule {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "Cannot archive a service account targeted by a live federation rule")
		return
	}
	f.archiveServiceAccount(sa.ID)
	writeFakeJSON(w, http.StatusOK, sa)
}

// seedServiceAccount and archiveServiceAccount simulate changes made outside
// Terraform. Callers outside ServeHTTP must hold mu.
func (f *fakeAdminAPI) seedWorkspaceServiceAccount(workspaceID, serviceAccountID, role string) {
	f.workspaceMembers[workspaceID+"/"+serviceAccountID] = &fakeServiceAccountMembership{
		ServiceAccountID: serviceAccountID,
		WorkspaceID:      workspaceID,
		WorkspaceRole:    role,
		CreatedByActorID: fakeActorID,
		Type:             "service_account_workspace_member",
	}
}

func (f *fakeAdminAPI) seedServiceAccount(name string) string {
	sa := f.newServiceAccount(name)
	f.serviceAccounts[sa.ID] = sa
	return sa.ID
}

func (f *fakeAdminAPI) archiveServiceAccount(id string) {
	archivedAt := fakeCreatedAt.Add(time.Hour).Format(time.RFC3339Nano)
	sa := f.serviceAccounts[id]
	sa.ArchivedAt = &archivedAt
	sa.ArchivedByActorID = fakeActorID
}

func (f *fakeAdminAPI) addWorkspaceServiceAccount(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspace_id")
	ws, ok := f.workspaces[workspaceID]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "Workspace not found")
		return
	}
	if ws.ArchivedAt != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "Workspace is archived")
		return
	}

	var body struct {
		ServiceAccountID string `json:"service_account_id"`
		WorkspaceRole    string `json:"workspace_role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	sa, ok := f.serviceAccounts[body.ServiceAccountID]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "Service account not found")
		return
	}
	if sa.ArchivedAt != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "Cannot add an archived service account to a workspace")
		return
	}

	key := workspaceID + "/" + body.ServiceAccountID
	if existing, ok := f.workspaceMembers[key]; ok {
		existing.WorkspaceRole = body.WorkspaceRole
		writeFakeJSON(w, http.StatusOK, existing)
		return
	}

	member := &fakeServiceAccountMembership{
		ServiceAccountID: body.ServiceAccountID,
		WorkspaceID:      workspaceID,
		WorkspaceRole:    body.WorkspaceRole,
		CreatedByActorID: fakeActorID,
		Type:             "service_account_workspace_member",
	}
	f.workspaceMembers[key] = member
	writeFakeJSON(w, http.StatusOK, member)
}

func (f *fakeAdminAPI) getWorkspaceServiceAccount(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspace_id")
	serviceAccountID := r.PathValue("service_account_id")

	ws, ok := f.workspaces[workspaceID]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "Workspace not found")
		return
	}
	if ws.ArchivedAt != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "Workspace is archived")
		return
	}

	if member, ok := f.workspaceMembers[workspaceID+"/"+serviceAccountID]; ok {
		writeFakeJSON(w, http.StatusOK, member)
		return
	}

	if sa, ok := f.serviceAccounts[serviceAccountID]; ok && sa.ArchivedAt == nil && workspaceID == f.defaultWorkspaceID {
		writeFakeJSON(w, http.StatusOK, &fakeServiceAccountMembership{
			ServiceAccountID: serviceAccountID,
			WorkspaceID:      workspaceID,
			WorkspaceRole:    "workspace_user",
			Implicit:         true,
			Type:             "service_account_workspace_member",
		})
		return
	}

	writeAPIError(w, http.StatusNotFound, "not_found_error", "Service account workspace membership not found")
}

func (f *fakeAdminAPI) updateWorkspaceServiceAccount(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspace_id")
	serviceAccountID := r.PathValue("service_account_id")

	ws, ok := f.workspaces[workspaceID]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "Workspace not found")
		return
	}
	if ws.ArchivedAt != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "Workspace is archived")
		return
	}

	member, ok := f.workspaceMembers[workspaceID+"/"+serviceAccountID]
	if !ok {
		if _, saOK := f.serviceAccounts[serviceAccountID]; saOK && workspaceID == f.defaultWorkspaceID {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "Implicit memberships cannot be updated; add the service account explicitly")
			return
		}
		writeAPIError(w, http.StatusNotFound, "not_found_error", "Service account workspace membership not found")
		return
	}

	var body struct {
		WorkspaceRole string `json:"workspace_role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}

	member.WorkspaceRole = body.WorkspaceRole
	writeFakeJSON(w, http.StatusOK, member)
}

func (f *fakeAdminAPI) removeWorkspaceServiceAccount(w http.ResponseWriter, r *http.Request) {
	workspaceID := r.PathValue("workspace_id")
	serviceAccountID := r.PathValue("service_account_id")

	ws, ok := f.workspaces[workspaceID]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "Workspace not found")
		return
	}
	if ws.ArchivedAt != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "Workspace is archived")
		return
	}

	delete(f.workspaceMembers, workspaceID+"/"+serviceAccountID)
	writeFakeJSON(w, http.StatusOK, map[string]any{
		"type":               "service_account_workspace_member_deleted",
		"service_account_id": serviceAccountID,
		"workspace_id":       workspaceID,
	})
}
