package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
)

// fakeWorkspaceMemberPageSize caps every member list page below the requested
// limit, so a test with more members exercises the after_id cursor.
const fakeWorkspaceMemberPageSize = 2

var fakeInheritedWorkspaceRoles = map[string]string{
	"admin":   "workspace_admin",
	"billing": "workspace_billing",
}

type fakeWorkspaceMember struct {
	Type          string `json:"type"`
	UserID        string `json:"user_id"`
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceRole string `json:"workspace_role"`
}

// fakeWorkspaceMembers holds user memberships apart from service account
// memberships, which carry different fields. Organization users live in
// f.users; an organization admin or billing member holds an inherited role in
// every workspace without an explicit row.
type fakeWorkspaceMembers struct {
	members map[string]*fakeWorkspaceMember
}

// registerWorkspaceMemberRoutes adds the workspace member endpoints to the
// fake's mux. Called once from newFakeAdminAPI.
func (f *fakeAdminAPI) registerWorkspaceMemberRoutes() {
	f.workspaceUsers = fakeWorkspaceMembers{
		members: make(map[string]*fakeWorkspaceMember),
	}

	f.mux.HandleFunc("POST /v1/organizations/workspaces/{workspace_id}/members", f.withLiveWorkspace(f.addWorkspaceMember))
	f.mux.HandleFunc("GET /v1/organizations/workspaces/{workspace_id}/members", f.withLiveWorkspace(f.listWorkspaceMembers))
	f.mux.HandleFunc("GET /v1/organizations/workspaces/{workspace_id}/members/{user_id}", f.withLiveWorkspace(f.getWorkspaceMember))
	f.mux.HandleFunc("POST /v1/organizations/workspaces/{workspace_id}/members/{user_id}", f.withLiveWorkspace(f.updateWorkspaceMember))
	f.mux.HandleFunc("DELETE /v1/organizations/workspaces/{workspace_id}/members/{user_id}", f.withLiveWorkspace(f.removeWorkspaceMember))
}

// withLiveWorkspace answers 400 for an archived workspace, matching the
// service account membership endpoints.
func (f *fakeAdminAPI) withLiveWorkspace(fn func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		fn(w, r, workspaceID)
	}
}

// seedOrganizationUser and seedWorkspaceMember simulate changes made outside
// Terraform. Callers outside ServeHTTP must hold mu.
func (f *fakeAdminAPI) seedOrganizationUser(organizationRole string) string {
	n := f.users.nextUserID + 1
	return f.seedUser(fmt.Sprintf("member%d@example.com", n), fmt.Sprintf("Member %d", n), organizationRole)
}

func (f *fakeAdminAPI) organizationRole(userID string) (string, bool) {
	user, ok := f.users.members[userID]
	if !ok {
		return "", false
	}
	return user.Role, true
}

// promoteOrganizationUser simulates an organization role change made outside
// Terraform. An inherited role replaces the user's explicit memberships.
func (f *fakeAdminAPI) promoteOrganizationUser(userID, organizationRole string) {
	f.users.members[userID].Role = organizationRole
	if _, ok := fakeInheritedWorkspaceRoles[organizationRole]; !ok {
		return
	}
	for key, member := range f.workspaceUsers.members {
		if member.UserID == userID {
			delete(f.workspaceUsers.members, key)
		}
	}
}

func (f *fakeAdminAPI) seedWorkspaceMember(workspaceID, userID, role string) {
	f.workspaceUsers.members[workspaceID+"/"+userID] = &fakeWorkspaceMember{
		Type:          "workspace_member",
		UserID:        userID,
		WorkspaceID:   workspaceID,
		WorkspaceRole: role,
	}
}

func (f *fakeAdminAPI) workspaceMember(workspaceID, userID string) (*fakeWorkspaceMember, bool) {
	if member, ok := f.workspaceUsers.members[workspaceID+"/"+userID]; ok {
		return member, true
	}
	organizationRole, _ := f.organizationRole(userID)
	if role, ok := fakeInheritedWorkspaceRoles[organizationRole]; ok {
		return &fakeWorkspaceMember{Type: "workspace_member", UserID: userID, WorkspaceID: workspaceID, WorkspaceRole: role}, true
	}
	return nil, false
}

func (f *fakeAdminAPI) inheritedWorkspaceRole(userID string) bool {
	organizationRole, _ := f.organizationRole(userID)
	_, ok := fakeInheritedWorkspaceRoles[organizationRole]
	return ok
}

func (f *fakeAdminAPI) addWorkspaceMember(w http.ResponseWriter, r *http.Request, workspaceID string) {
	var body struct {
		UserID        string `json:"user_id"`
		WorkspaceRole string `json:"workspace_role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fakeInvalid(w, "%s", err)
		return
	}
	if body.WorkspaceRole == "workspace_billing" {
		fakeInvalid(w, "workspace_role: cannot be workspace_billing")
		return
	}
	if _, ok := f.organizationRole(body.UserID); !ok {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "User not found")
		return
	}
	if f.inheritedWorkspaceRole(body.UserID) {
		fakeInvalid(w, "Organization admins and billing members inherit their workspace role")
		return
	}

	f.seedWorkspaceMember(workspaceID, body.UserID, body.WorkspaceRole)
	writeFakeJSON(w, http.StatusOK, f.workspaceUsers.members[workspaceID+"/"+body.UserID])
}

func (f *fakeAdminAPI) getWorkspaceMember(w http.ResponseWriter, r *http.Request, workspaceID string) {
	member, ok := f.workspaceMember(workspaceID, r.PathValue("user_id"))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "Workspace member not found")
		return
	}
	writeFakeJSON(w, http.StatusOK, member)
}

func (f *fakeAdminAPI) updateWorkspaceMember(w http.ResponseWriter, r *http.Request, workspaceID string) {
	userID := r.PathValue("user_id")
	member, ok := f.workspaceUsers.members[workspaceID+"/"+userID]
	if !ok {
		if f.inheritedWorkspaceRole(userID) {
			fakeInvalid(w, "Organization admins and billing members inherit their workspace role")
			return
		}
		writeAPIError(w, http.StatusNotFound, "not_found_error", "Workspace member not found")
		return
	}

	var body struct {
		WorkspaceRole string `json:"workspace_role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fakeInvalid(w, "%s", err)
		return
	}
	if body.WorkspaceRole == "workspace_billing" {
		fakeInvalid(w, "The workspace_billing role cannot be manually assigned")
		return
	}

	member.WorkspaceRole = body.WorkspaceRole
	writeFakeJSON(w, http.StatusOK, member)
}

func (f *fakeAdminAPI) removeWorkspaceMember(w http.ResponseWriter, r *http.Request, workspaceID string) {
	userID := r.PathValue("user_id")
	key := workspaceID + "/" + userID
	if _, ok := f.workspaceUsers.members[key]; !ok {
		if f.inheritedWorkspaceRole(userID) {
			fakeInvalid(w, "Organization admins and billing members cannot be removed from workspaces")
			return
		}
		writeAPIError(w, http.StatusNotFound, "not_found_error", "Workspace member not found")
		return
	}

	delete(f.workspaceUsers.members, key)
	writeFakeJSON(w, http.StatusOK, map[string]any{
		"type":         "workspace_member_deleted",
		"user_id":      userID,
		"workspace_id": workspaceID,
	})
}

func (f *fakeAdminAPI) listWorkspaceMembers(w http.ResponseWriter, r *http.Request, workspaceID string) {
	userIDs := make([]string, 0, len(f.users.members))
	for userID := range f.users.members {
		if _, ok := f.workspaceMember(workspaceID, userID); ok {
			userIDs = append(userIDs, userID)
		}
	}
	slices.Sort(userIDs)

	start := 0
	if afterID := r.URL.Query().Get("after_id"); afterID != "" {
		start = slices.Index(userIDs, afterID) + 1
	}
	end := min(start+fakeWorkspaceMemberPageSize, len(userIDs))
	page := userIDs[start:end]

	data := make([]*fakeWorkspaceMember, 0, len(page))
	for _, userID := range page {
		member, _ := f.workspaceMember(workspaceID, userID)
		data = append(data, member)
	}
	body := map[string]any{"data": data, "has_more": end < len(userIDs), "first_id": nil, "last_id": nil}
	if len(page) > 0 {
		body["first_id"] = page[0]
		body["last_id"] = page[len(page)-1]
	}
	writeFakeJSON(w, http.StatusOK, body)
}
