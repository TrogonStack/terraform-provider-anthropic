package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"time"
)

type fakeRBAC struct {
	nextGroupID int
	nextRoleID  int
	groups      map[string]*fakeRBACGroup
	members     map[string][]fakeRBACGroupMember
	users       map[string]string
	roles       []*fakeRBACRole
	// roleDataUnavailable makes every group answer role_ids as null, which
	// the API documents as role data being temporarily unavailable.
	roleDataUnavailable bool
}

type fakeRBACGroup struct {
	ID         string
	Name       string
	SourceType string
	RoleIDs    []string
	CreatedAt  string
	UpdatedAt  string
}

type fakeRBACGroupMember struct {
	UserID    string
	CreatedAt string
}

type fakeRBACRole struct {
	ID          string
	DisplayName string
	Permissions []map[string]any
}

func (f *fakeAdminAPI) rbacGroupJSON(group *fakeRBACGroup) map[string]any {
	var roleIDs any = append([]string{}, group.RoleIDs...)
	if f.rbac.roleDataUnavailable {
		roleIDs = nil
	}
	return map[string]any{
		"type":        "rbac_group",
		"id":          group.ID,
		"name":        group.Name,
		"source_type": group.SourceType,
		"role_ids":    roleIDs,
		"created_at":  group.CreatedAt,
		"updated_at":  group.UpdatedAt,
	}
}

func (f *fakeAdminAPI) rbacGroupMemberJSON(groupID string, member fakeRBACGroupMember) map[string]any {
	return map[string]any{
		"type":          "rbac_group_member",
		"rbac_group_id": groupID,
		"user_id":       member.UserID,
		"email":         f.rbac.users[member.UserID],
		"created_at":    member.CreatedAt,
	}
}

// registerRBACGroupRoutes adds the RBAC group, group member, role and role
// permission endpoints to the fake's mux. Called once from newFakeAdminAPI.
func (f *fakeAdminAPI) registerRBACGroupRoutes() {
	f.rbac = &fakeRBAC{
		groups:  make(map[string]*fakeRBACGroup),
		members: make(map[string][]fakeRBACGroupMember),
		users:   make(map[string]string),
	}

	f.mux.HandleFunc("POST /v1/organizations/rbac_groups", withRBACBeta(f.createRBACGroup))
	f.mux.HandleFunc("GET /v1/organizations/rbac_groups/{id}", withRBACBeta(f.withRBACGroup(f.getRBACGroup)))
	f.mux.HandleFunc("POST /v1/organizations/rbac_groups/{id}", withRBACBeta(f.withRBACGroup(f.updateRBACGroup)))
	f.mux.HandleFunc("DELETE /v1/organizations/rbac_groups/{id}", withRBACBeta(f.withRBACGroup(f.deleteRBACGroup)))
	f.mux.HandleFunc("GET /v1/organizations/rbac_groups/{id}/members", withRBACBeta(f.withRBACGroup(f.listRBACGroupMembers)))
	f.mux.HandleFunc("POST /v1/organizations/rbac_groups/{id}/members", withRBACBeta(f.withRBACGroup(f.addRBACGroupMember)))
	f.mux.HandleFunc("DELETE /v1/organizations/rbac_groups/{id}/members/{user_id}", withRBACBeta(f.withRBACGroup(f.removeRBACGroupMember)))
	f.mux.HandleFunc("GET /v1/organizations/rbac_roles", withRBACBeta(f.listRBACRoles))
	f.mux.HandleFunc("GET /v1/organizations/rbac_roles/{id}/permissions", withRBACBeta(f.listRBACRolePermissions))
}

// withRBACBeta asserts that the provider calls the SDK's Beta surface, whose
// paths carry beta=true. It does not claim the real API rejects other calls.
func withRBACBeta(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("beta") != "true" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "the fake serves RBAC endpoints only through the Beta SDK surface")
			return
		}
		fn(w, r)
	}
}

func (f *fakeAdminAPI) withRBACGroup(fn func(http.ResponseWriter, *http.Request, *fakeRBACGroup)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		group, ok := f.rbac.groups[r.PathValue("id")]
		if !ok {
			writeAPIError(w, http.StatusNotFound, "not_found_error", "RBAC group not found")
			return
		}
		fn(w, r, group)
	}
}

func rejectSCIMGroup(w http.ResponseWriter, group *fakeRBACGroup) bool {
	if group.SourceType != "scim" {
		return false
	}
	writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "RBAC groups provisioned by SCIM cannot be modified through the API")
	return true
}

func (f *fakeAdminAPI) createRBACGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if body.Name == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "name: Field required")
		return
	}
	group := f.seedRBACGroup(body.Name, "direct")
	writeFakeJSON(w, http.StatusOK, f.rbacGroupJSON(f.rbac.groups[group]))
}

func (f *fakeAdminAPI) getRBACGroup(w http.ResponseWriter, _ *http.Request, group *fakeRBACGroup) {
	writeFakeJSON(w, http.StatusOK, f.rbacGroupJSON(group))
}

func (f *fakeAdminAPI) updateRBACGroup(w http.ResponseWriter, r *http.Request, group *fakeRBACGroup) {
	if rejectSCIMGroup(w, group) {
		return
	}
	var body struct {
		Name *string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if body.Name != nil {
		group.Name = *body.Name
	}
	group.UpdatedAt = fakeCreatedAt.Add(time.Hour).Format(time.RFC3339Nano)
	writeFakeJSON(w, http.StatusOK, f.rbacGroupJSON(group))
}

func (f *fakeAdminAPI) deleteRBACGroup(w http.ResponseWriter, _ *http.Request, group *fakeRBACGroup) {
	if rejectSCIMGroup(w, group) {
		return
	}
	f.removeRBACGroup(group.ID)
	writeFakeJSON(w, http.StatusOK, map[string]any{"type": "rbac_group_deleted", "id": group.ID})
}

func (f *fakeAdminAPI) listRBACGroupMembers(w http.ResponseWriter, r *http.Request, group *fakeRBACGroup) {
	current := []fakeRBACGroupMember{}
	for _, member := range f.rbac.members[group.ID] {
		if _, ok := f.rbac.users[member.UserID]; ok {
			current = append(current, member)
		}
	}
	fakePage(w, r, current, func(member fakeRBACGroupMember) any { return f.rbacGroupMemberJSON(group.ID, member) })
}

func (f *fakeAdminAPI) addRBACGroupMember(w http.ResponseWriter, r *http.Request, group *fakeRBACGroup) {
	var body struct {
		UserID string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if rejectSCIMGroup(w, group) {
		return
	}
	if _, ok := f.rbac.users[body.UserID]; !ok {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "User is not a member of the enterprise's organizations")
		return
	}
	if f.rbacGroupMemberIndex(group.ID, body.UserID) >= 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "User is already a member of the RBAC group")
		return
	}
	member := f.seedRBACGroupMember(group.ID, body.UserID)
	writeFakeJSON(w, http.StatusOK, f.rbacGroupMemberJSON(group.ID, member))
}

func (f *fakeAdminAPI) removeRBACGroupMember(w http.ResponseWriter, r *http.Request, group *fakeRBACGroup) {
	if rejectSCIMGroup(w, group) {
		return
	}
	userID := r.PathValue("user_id")
	if !f.deleteRBACGroupMember(group.ID, userID) {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "User is not a member of the RBAC group")
		return
	}
	writeFakeJSON(w, http.StatusOK, map[string]any{"type": "rbac_group_member_deleted", "rbac_group_id": group.ID, "user_id": userID})
}

func (f *fakeAdminAPI) listRBACRoles(w http.ResponseWriter, r *http.Request) {
	fakePage(w, r, f.rbac.roles, func(role *fakeRBACRole) any {
		return map[string]any{
			"type":         "rbac_role",
			"id":           role.ID,
			"display_name": role.DisplayName,
			"created_at":   fakeCreatedAt.Format(time.RFC3339Nano),
			"updated_at":   fakeCreatedAt.Add(time.Hour).Format(time.RFC3339Nano),
		}
	})
}

func (f *fakeAdminAPI) listRBACRolePermissions(w http.ResponseWriter, r *http.Request) {
	index := slices.IndexFunc(f.rbac.roles, func(role *fakeRBACRole) bool { return role.ID == r.PathValue("id") })
	if index < 0 {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "RBAC role not found")
		return
	}
	fakePage(w, r, f.rbac.roles[index].Permissions, func(permission map[string]any) any { return permission })
}

func (f *fakeAdminAPI) rbacGroupMemberIndex(groupID, userID string) int {
	return slices.IndexFunc(f.rbac.members[groupID], func(member fakeRBACGroupMember) bool { return member.UserID == userID })
}

// The seed and remove helpers below simulate changes made outside Terraform.
// Callers outside ServeHTTP must hold mu.
func (f *fakeAdminAPI) seedRBACUser(userID, email string) {
	f.rbac.users[userID] = email
}

func (f *fakeAdminAPI) seedRBACGroup(name, sourceType string, roleIDs ...string) string {
	f.rbac.nextGroupID++
	group := &fakeRBACGroup{
		ID:         fmt.Sprintf("rbac_group_%04d", f.rbac.nextGroupID),
		Name:       name,
		SourceType: sourceType,
		RoleIDs:    roleIDs,
		CreatedAt:  fakeCreatedAt.Format(time.RFC3339Nano),
		UpdatedAt:  fakeCreatedAt.Format(time.RFC3339Nano),
	}
	f.rbac.groups[group.ID] = group
	return group.ID
}

func (f *fakeAdminAPI) seedRBACGroupMember(groupID, userID string) fakeRBACGroupMember {
	member := fakeRBACGroupMember{UserID: userID, CreatedAt: fakeCreatedAt.Add(2 * time.Hour).Format(time.RFC3339Nano)}
	f.rbac.members[groupID] = append(f.rbac.members[groupID], member)
	return member
}

func (f *fakeAdminAPI) seedRBACRole(displayName string, permissions ...map[string]any) string {
	f.rbac.nextRoleID++
	role := &fakeRBACRole{
		ID:          fmt.Sprintf("rbac_role_%04d", f.rbac.nextRoleID),
		DisplayName: displayName,
		Permissions: permissions,
	}
	f.rbac.roles = append(f.rbac.roles, role)
	return role.ID
}

func (f *fakeAdminAPI) removeRBACGroup(groupID string) {
	delete(f.rbac.groups, groupID)
	delete(f.rbac.members, groupID)
}

func (f *fakeAdminAPI) deleteRBACGroupMember(groupID, userID string) bool {
	index := f.rbacGroupMemberIndex(groupID, userID)
	if index < 0 {
		return false
	}
	f.rbac.members[groupID] = slices.Delete(f.rbac.members[groupID], index, index+1)
	return true
}

func fakeRBACPermission(action, resourceType string, identifiers map[string]string) map[string]any {
	resource := map[string]any{"type": resourceType}
	for key, value := range identifiers {
		resource[key] = value
	}
	return map[string]any{"type": "rbac_role_permission", "action": action, "resource": resource}
}
