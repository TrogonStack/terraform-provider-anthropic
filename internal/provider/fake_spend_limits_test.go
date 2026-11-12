package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	fakeSpendLimitCurrency = "USD"

	fakeSpendLimitMemberAda     = "user_example_ada"
	fakeSpendLimitMemberGrace   = "user_example_grace"
	fakeSpendLimitMemberKatie   = "user_example_katie"
	fakeSpendLimitMemberDeleted = "user_example_deleted"
)

var fakeSpendLimitAmountPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

type fakeSpendLimitMember struct {
	UserID   string
	Name     *string
	Email    *string
	Deleted  bool
	SeatTier string
}

type fakeSpendLimit struct {
	ID        string
	ScopeType string
	ScopeKey  string
	Period    string
	Amount    *string
	CreatedAt string
	UpdatedAt string
}

func (l *fakeSpendLimit) scopeJSON() map[string]any {
	switch l.ScopeType {
	case "seat_tier":
		return map[string]any{"type": "seat_tier", "seat_tier": l.ScopeKey}
	case "organization":
		return map[string]any{"type": "organization"}
	default:
		return map[string]any{"type": "user", "user_id": l.ScopeKey}
	}
}

func (l *fakeSpendLimit) toJSON() map[string]any {
	return map[string]any{
		"type":       "spend_limit",
		"id":         l.ID,
		"amount":     l.Amount,
		"currency":   fakeSpendLimitCurrency,
		"is_enabled": true,
		"period":     l.Period,
		"scope":      l.scopeJSON(),
		"created_at": l.CreatedAt,
		"updated_at": l.UpdatedAt,
	}
}

// fakeSpendLimits holds a Claude Enterprise organization's spend limits: the
// per-user overrides the API sets, and the seat tier and organization defaults
// that only claude.ai configures.
type fakeSpendLimits struct {
	nextID    int
	clock     int
	members   []fakeSpendLimitMember
	overrides map[string]*fakeSpendLimit
	defaults  []*fakeSpendLimit
	setBodies []string
	// deleteRaces makes DELETE remove the override and still answer 404, as
	// when another client deletes it between a refresh and the delete.
	deleteRaces bool
}

func fakeStringPointer(s string) *string { return &s }

func (f *fakeAdminAPI) registerSpendLimitRoutes() {
	f.spendLimits = &fakeSpendLimits{
		members: []fakeSpendLimitMember{
			{UserID: fakeSpendLimitMemberAda, Name: fakeStringPointer("Ada"), Email: fakeStringPointer("ada@example.com"), SeatTier: "premium"},
			{UserID: fakeSpendLimitMemberDeleted, Deleted: true, SeatTier: "standard"},
			{UserID: fakeSpendLimitMemberGrace, Name: fakeStringPointer("Grace"), Email: fakeStringPointer("grace@example.com"), SeatTier: "standard"},
			{UserID: fakeSpendLimitMemberKatie, Email: fakeStringPointer("katie@example.com"), SeatTier: "standard"},
		},
		overrides: map[string]*fakeSpendLimit{},
		defaults: []*fakeSpendLimit{
			{ID: "spl_example_premium_monthly", ScopeType: "seat_tier", ScopeKey: "premium", Period: "monthly", Amount: fakeStringPointer("50000")},
			{ID: "spl_example_organization_monthly", ScopeType: "organization", Period: "monthly", Amount: fakeStringPointer("20000")},
			{ID: "spl_example_organization_weekly", ScopeType: "organization", Period: "weekly", Amount: fakeStringPointer("7500")},
		},
	}
	for _, limit := range f.spendLimits.defaults {
		limit.CreatedAt = fakeCreatedAt.Format(time.RFC3339)
		limit.UpdatedAt = limit.CreatedAt
	}

	f.mux.HandleFunc("POST /v1/organizations/spend_limits", f.setSpendLimit)
	f.mux.HandleFunc("GET /v1/organizations/spend_limits/effective", f.listEffectiveSpendLimits)
	f.mux.HandleFunc("GET /v1/organizations/spend_limits/{id}", f.getSpendLimit)
	f.mux.HandleFunc("DELETE /v1/organizations/spend_limits/{id}", f.deleteSpendLimit)
}

func (s *fakeSpendLimits) now() string {
	s.clock++
	return fakeCreatedAt.Add(time.Duration(s.clock) * time.Minute).Format(time.RFC3339)
}

func (s *fakeSpendLimits) member(userID string) (fakeSpendLimitMember, bool) {
	for _, member := range s.members {
		if member.UserID == userID {
			return member, true
		}
	}
	return fakeSpendLimitMember{}, false
}

func (s *fakeSpendLimits) override(userID, period string) *fakeSpendLimit {
	for _, limit := range s.overrides {
		if limit.ScopeKey == userID && limit.Period == period {
			return limit
		}
	}
	return nil
}

// upsert overwrites the override for the user and period in place, keeping
// its ID, as the API documents for Set.
func (s *fakeSpendLimits) upsert(userID, period string, amount *string) *fakeSpendLimit {
	if limit := s.override(userID, period); limit != nil {
		limit.Amount = amount
		limit.UpdatedAt = s.now()
		return limit
	}
	s.nextID++
	createdAt := s.now()
	limit := &fakeSpendLimit{
		ID:        fmt.Sprintf("spl_%024d", s.nextID),
		ScopeType: "user",
		ScopeKey:  userID,
		Period:    period,
		Amount:    amount,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
	s.overrides[limit.ID] = limit
	return limit
}

// effective resolves a member's limit for a period from the override, then
// the seat tier default, then the organization default.
func (s *fakeSpendLimits) effective(member fakeSpendLimitMember, period string) *fakeSpendLimit {
	if limit := s.override(member.UserID, period); limit != nil {
		return limit
	}
	for _, scopeType := range []string{"seat_tier", "organization"} {
		for _, limit := range s.defaults {
			if limit.ScopeType == scopeType && limit.Period == period && (scopeType == "organization" || limit.ScopeKey == member.SeatTier) {
				return limit
			}
		}
	}
	return nil
}

func (f *fakeAdminAPI) setSpendLimit(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		fakeInvalid(w, "%s", err)
		return
	}
	f.spendLimits.setBodies = append(f.spendLimits.setBodies, string(raw))

	var body struct {
		Amount json.RawMessage `json:"amount"`
		Scope  *struct {
			Type   string `json:"type"`
			UserID string `json:"user_id"`
		} `json:"scope"`
		Period *string `json:"period"`
	}
	if !decodeFakeJSON(w, raw, &body) {
		return
	}
	if body.Amount == nil {
		fakeInvalid(w, "amount: Field required")
		return
	}
	var amount *string
	if err := json.Unmarshal(body.Amount, &amount); err != nil {
		fakeInvalid(w, "amount: must be a string or null")
		return
	}
	if amount != nil && !fakeSpendLimitAmountPattern.MatchString(*amount) {
		fakeInvalid(w, "amount: must be a non-negative integer decimal string")
		return
	}
	if body.Scope == nil {
		fakeInvalid(w, "scope: Field required")
		return
	}
	if body.Scope.Type != "user" {
		fakeInvalid(w, "scope: a Claude Enterprise organization sets only user spend limits")
		return
	}
	if _, ok := f.spendLimits.member(body.Scope.UserID); !ok {
		fakeInvalid(w, "scope.user_id: %s is not a member of the organization", body.Scope.UserID)
		return
	}
	period := "monthly"
	if body.Period != nil {
		period = *body.Period
	}
	if period != "monthly" {
		fakeInvalid(w, "period: only monthly is supported")
		return
	}
	writeFakeJSON(w, http.StatusOK, f.spendLimits.upsert(body.Scope.UserID, period, amount).toJSON())
}

func (f *fakeAdminAPI) getSpendLimit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if limit, ok := f.spendLimits.overrides[id]; ok {
		writeFakeJSON(w, http.StatusOK, limit.toJSON())
		return
	}
	for _, limit := range f.spendLimits.defaults {
		if limit.ID == id {
			writeFakeJSON(w, http.StatusOK, limit.toJSON())
			return
		}
	}
	writeAPIError(w, http.StatusNotFound, "not_found_error", "Spend limit not found")
}

func (f *fakeAdminAPI) deleteSpendLimit(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := f.spendLimits.overrides[id]; !ok || f.spendLimits.deleteRaces {
		delete(f.spendLimits.overrides, id)
		writeAPIError(w, http.StatusNotFound, "not_found_error", "Spend limit not found")
		return
	}
	delete(f.spendLimits.overrides, id)
	writeFakeJSON(w, http.StatusOK, map[string]any{"id": id, "type": "spend_limit_deleted"})
}

// listEffectiveSpendLimits pages by member, fakeTelemetryPageSize members a
// page, so a page carries every period row of its members.
func (f *fakeAdminAPI) listEffectiveSpendLimits(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	userIDs := query["user_ids[]"]
	periods := query["period[]"]
	if len(userIDs) > 100 {
		fakeInvalid(w, "user_ids: at most 100 entries")
		return
	}
	if len(periods) > 3 {
		fakeInvalid(w, "period: at most 3 entries")
		return
	}
	if len(periods) == 0 {
		periods = []string{"daily", "weekly", "monthly"}
	}

	members := []fakeSpendLimitMember{}
	for _, member := range f.spendLimits.members {
		if len(userIDs) == 0 || slices.Contains(userIDs, member.UserID) {
			members = append(members, member)
		}
	}

	start := 0
	if cursor := query.Get("page"); cursor != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(cursor, "cursor_"))
		if err != nil {
			fakeInvalid(w, "page: invalid cursor")
			return
		}
		start = n
	}
	end := min(start+fakeTelemetryPageSize, len(members))
	data := []any{}
	for _, member := range members[min(start, len(members)):end] {
		for _, period := range periods {
			if limit := f.spendLimits.effective(member, period); limit != nil {
				data = append(data, fakeSpendSummaryJSON(member, limit))
			}
		}
	}
	var next any
	if end < len(members) {
		next = fmt.Sprintf("cursor_%d", end)
	}
	writeFakeJSON(w, http.StatusOK, map[string]any{"data": data, "next_page": next})
}

func fakeSpendSummaryJSON(member fakeSpendLimitMember, limit *fakeSpendLimit) map[string]any {
	return map[string]any{
		"actor": map[string]any{
			"type":          "user_actor",
			"user_id":       member.UserID,
			"name":          member.Name,
			"email_address": member.Email,
			"deleted":       member.Deleted,
		},
		"amount":               limit.Amount,
		"currency":             fakeSpendLimitCurrency,
		"period":               limit.Period,
		"period_to_date_spend": "1250.5",
		"scope":                map[string]any{"type": "user", "user_id": member.UserID},
		"source":               limit.scopeJSON(),
		"spend_limit_id":       limit.ID,
	}
}

// setSpendLimitOutOfBand changes an override as another client would.
// Callers hold f.mu.
func (f *fakeAdminAPI) setSpendLimitOutOfBand(userID, period string, amount *string) *fakeSpendLimit {
	return f.spendLimits.upsert(userID, period, amount)
}

func (f *fakeAdminAPI) spendLimitOverride(userID, period string) (fakeSpendLimit, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if limit := f.spendLimits.override(userID, period); limit != nil {
		return *limit, true
	}
	return fakeSpendLimit{}, false
}

func (f *fakeAdminAPI) lastSpendLimitSetBody() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.spendLimits.setBodies) == 0 {
		return ""
	}
	return f.spendLimits.setBodies[len(f.spendLimits.setBodies)-1]
}
