package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
)

// fakeUserPageSize caps every member list page below any requested limit, so
// a test that seeds more members exercises ListAutoPaging's after_id cursor.
const fakeUserPageSize = 2

var fakeInviteLifetime = 21 * 24 * time.Hour

var fakeAdministrativeRoles = []string{"owner", "membership_admin", "primary_owner"}

var fakeAssignableRoles = []string{"user", "managed"}

type fakeUser struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	AddedAt string `json:"added_at"`
}

type fakeInvite struct {
	ID           string   `json:"id"`
	Type         string   `json:"type"`
	Email        string   `json:"email"`
	Role         string   `json:"role"`
	RBACGroupIDs []string `json:"rbac_group_ids"`
	Status       string   `json:"status"`
	InvitedAt    string   `json:"invited_at"`
	ExpiresAt    string   `json:"expires_at"`
	AcceptedAt   *string  `json:"accepted_at"`
}

// fakeUsers models a Claude Enterprise organization's members and invites:
// only user and managed are assignable, and administrative members are fixed.
type fakeUsers struct {
	nextUserID     int
	nextInviteID   int
	members        map[string]*fakeUser
	invites        map[string]*fakeInvite
	inviteRequests []string
	removedUsers   []string
}

func (f *fakeAdminAPI) registerUserRoutes() {
	f.users = &fakeUsers{
		members: make(map[string]*fakeUser),
		invites: make(map[string]*fakeInvite),
	}
	f.mux.HandleFunc("GET /v1/organizations/users", f.listUsers)
	f.mux.HandleFunc("GET /v1/organizations/users/{id}", f.withUser(f.getUser))
	f.mux.HandleFunc("POST /v1/organizations/users/{id}", f.withUser(f.updateUser))
	f.mux.HandleFunc("DELETE /v1/organizations/users/{id}", f.withUser(f.removeUser))
	f.mux.HandleFunc("POST /v1/organizations/invites", f.createInvite)
	f.mux.HandleFunc("GET /v1/organizations/invites/{id}", f.withInvite(f.getInvite))
	f.mux.HandleFunc("DELETE /v1/organizations/invites/{id}", f.withInvite(f.deleteInvite))
}

// seedUser plants a member as if added in the organization settings.
// Callers outside ServeHTTP must hold mu.
func (f *fakeAdminAPI) seedUser(email, name, role string) string {
	f.users.nextUserID++
	id := fmt.Sprintf("user_%04d", f.users.nextUserID)
	f.users.members[id] = &fakeUser{
		ID:      id,
		Type:    "user",
		Email:   email,
		Name:    name,
		Role:    role,
		AddedAt: fakeCreatedAt.Add(time.Duration(f.users.nextUserID) * time.Minute).Format(time.RFC3339Nano),
	}
	return id
}

// setInviteStatus simulates the recipient accepting, or the invite expiring.
// Callers outside ServeHTTP must hold mu.
func (f *fakeAdminAPI) setInviteStatus(id, status string) {
	invite := f.users.invites[id]
	invite.Status = status
	if status == "accepted" {
		acceptedAt := fakeCreatedAt.Add(time.Hour).Format(time.RFC3339Nano)
		invite.AcceptedAt = &acceptedAt
	}
}

func (f *fakeAdminAPI) withUser(fn func(http.ResponseWriter, *http.Request, *fakeUser)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := f.users.members[r.PathValue("id")]
		if !ok {
			writeAPIError(w, http.StatusNotFound, "not_found_error", "User not found")
			return
		}
		fn(w, r, user)
	}
}

func (f *fakeAdminAPI) withInvite(fn func(http.ResponseWriter, *http.Request, *fakeInvite)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		invite, ok := f.users.invites[r.PathValue("id")]
		if !ok {
			writeAPIError(w, http.StatusNotFound, "not_found_error", "Invite not found")
			return
		}
		fn(w, r, invite)
	}
}

func (f *fakeAdminAPI) getUser(w http.ResponseWriter, _ *http.Request, user *fakeUser) {
	writeFakeJSON(w, http.StatusOK, user)
}

func (f *fakeAdminAPI) updateUser(w http.ResponseWriter, r *http.Request, user *fakeUser) {
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	if slices.Contains(fakeAdministrativeRoles, user.Role) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "Members with an administrative role cannot be modified through the API")
		return
	}
	if !slices.Contains(fakeAssignableRoles, body.Role) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("role: %q cannot be assigned through the API", body.Role))
		return
	}
	user.Role = body.Role
	writeFakeJSON(w, http.StatusOK, user)
}

func (f *fakeAdminAPI) removeUser(w http.ResponseWriter, _ *http.Request, user *fakeUser) {
	delete(f.users.members, user.ID)
	f.users.removedUsers = append(f.users.removedUsers, user.ID)
	writeFakeJSON(w, http.StatusOK, map[string]any{"id": user.ID, "type": "user_deleted"})
}

func (f *fakeAdminAPI) listUsers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	email := q.Get("email")
	roles := q["roles[]"]
	afterID := q.Get("after_id")

	all := make([]*fakeUser, 0, len(f.users.members))
	for _, user := range f.users.members {
		all = append(all, user)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].AddedAt > all[j].AddedAt })

	var filtered []*fakeUser
	for _, user := range all {
		if email != "" && !strings.EqualFold(user.Email, email) {
			continue
		}
		if len(roles) > 0 && !slices.Contains(roles, user.Role) {
			continue
		}
		filtered = append(filtered, user)
	}

	start := 0
	if afterID != "" {
		for i, user := range filtered {
			if user.ID == afterID {
				start = i + 1
				break
			}
		}
	}
	end := min(start+fakeUserPageSize, len(filtered))
	page := filtered[start:end]

	body := map[string]any{
		"data":     page,
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

func (f *fakeAdminAPI) createInvite(w http.ResponseWriter, r *http.Request) {
	raw := new(strings.Builder)
	var body struct {
		Email        string   `json:"email"`
		Role         string   `json:"role"`
		RBACGroupIDs []string `json:"rbac_group_ids"`
	}
	if err := json.NewDecoder(io.TeeReader(r.Body, raw)).Decode(&body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	f.users.inviteRequests = append(f.users.inviteRequests, strings.TrimSpace(raw.String()))
	if !strings.Contains(body.Email, "@") {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "email: value is not a valid email address")
		return
	}
	if !slices.Contains(fakeAssignableRoles, body.Role) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("role: %q cannot be assigned through the API", body.Role))
		return
	}
	for _, invite := range f.users.invites {
		if invite.Status == "pending" && strings.EqualFold(invite.Email, body.Email) {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("A pending invite already exists for this email: %s", invite.ID))
			return
		}
	}
	for _, user := range f.users.members {
		if strings.EqualFold(user.Email, body.Email) {
			writeAPIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("This email already belongs to a member: %s", user.ID))
			return
		}
	}

	groups := body.RBACGroupIDs
	if groups == nil {
		groups = []string{}
	}
	f.users.nextInviteID++
	invitedAt := fakeCreatedAt.Add(time.Duration(f.users.nextInviteID) * time.Minute)
	invite := &fakeInvite{
		ID:           fmt.Sprintf("invite_%04d", f.users.nextInviteID),
		Type:         "invite",
		Email:        strings.ToLower(body.Email),
		Role:         body.Role,
		RBACGroupIDs: groups,
		Status:       "pending",
		InvitedAt:    invitedAt.Format(time.RFC3339Nano),
		ExpiresAt:    invitedAt.Add(fakeInviteLifetime).Format(time.RFC3339Nano),
	}
	f.users.invites[invite.ID] = invite
	writeFakeJSON(w, http.StatusOK, invite)
}

func (f *fakeAdminAPI) getInvite(w http.ResponseWriter, _ *http.Request, invite *fakeInvite) {
	writeFakeJSON(w, http.StatusOK, invite)
}

func (f *fakeAdminAPI) deleteInvite(w http.ResponseWriter, _ *http.Request, invite *fakeInvite) {
	switch invite.Status {
	case "accepted":
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", "Cannot withdraw an accepted invite; remove the member instead")
		return
	case "expired", "deleted":
		writeAPIError(w, http.StatusBadRequest, "invalid_request_error", fmt.Sprintf("Cannot withdraw an invite that is %s", invite.Status))
		return
	}
	invite.Status = "deleted"
	writeFakeJSON(w, http.StatusOK, map[string]any{"id": invite.ID, "type": "invite_deleted"})
}
