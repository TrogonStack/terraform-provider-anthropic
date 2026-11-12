package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const fakePluginsBeta = "ce-plugins-2026-09-01"

var fakeInstallationPreferences = []string{"required", "auto_install", "available", "not_available"}

type fakePlugin struct {
	ID            string
	Name          string
	MarketplaceID string
	OwnerUserID   string
	CreatedAt     time.Time
	settings      map[string]*fakePluginInstallationSetting
}

type fakePluginInstallationSetting struct {
	Target     string
	Preference string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type fakePluginMarketplace struct {
	ID          string
	Name        string
	OwnerUserID string
	Source      string
	Default     string
	CreatedAt   time.Time
}

// fakePluginState models the Plugins API beta. A plugin or marketplace with an
// OwnerUserID is member-owned, which the installation setting and marketplace
// default endpoints refuse.
type fakePluginState struct {
	clock        int
	plugins      map[string]*fakePlugin
	marketplaces map[string]*fakePluginMarketplace
	rbacGroups   map[string]bool
}

func (f *fakeAdminAPI) registerPluginRoutes() {
	f.plugins = &fakePluginState{
		plugins:      map[string]*fakePlugin{},
		marketplaces: map[string]*fakePluginMarketplace{},
		rbacGroups:   map[string]bool{},
	}
	f.mux.HandleFunc("GET /v1/organizations/plugins", f.withPluginsBeta(f.listPlugins))
	f.mux.HandleFunc("GET /v1/organizations/plugins/{plugin_id}/installation_settings", f.withPluginsBeta(f.withOrganizationPlugin(f.listPluginInstallationSettings)))
	f.mux.HandleFunc("POST /v1/organizations/plugins/{plugin_id}/installation_settings/{target}", f.withPluginsBeta(f.withPluginTarget(f.setPluginInstallationSetting)))
	f.mux.HandleFunc("DELETE /v1/organizations/plugins/{plugin_id}/installation_settings/{target}", f.withPluginsBeta(f.withPluginTarget(f.removePluginInstallationSetting)))
	f.mux.HandleFunc("GET /v1/organizations/plugin_marketplaces", f.withPluginsBeta(f.listPluginMarketplaces))
	f.mux.HandleFunc("GET /v1/organizations/plugin_marketplaces/{marketplace_id}", f.withPluginsBeta(f.withPluginMarketplace(f.getPluginMarketplace)))
	f.mux.HandleFunc("POST /v1/organizations/plugin_marketplaces/{marketplace_id}", f.withPluginsBeta(f.withPluginMarketplace(f.updatePluginMarketplace)))
}

func (s *fakePluginState) tick() time.Time {
	s.clock++
	return fakeCreatedAt.Add(time.Duration(s.clock) * time.Minute)
}

func (f *fakeAdminAPI) seedPluginMarketplace(name, ownerUserID, source string) string {
	s := f.plugins
	id := fmt.Sprintf("marketplace_%04d", len(s.marketplaces)+1)
	s.marketplaces[id] = &fakePluginMarketplace{ID: id, Name: name, OwnerUserID: ownerUserID, Source: source, CreatedAt: s.tick()}
	return id
}

func (f *fakeAdminAPI) seedPlugin(name, marketplaceID, ownerUserID string) string {
	s := f.plugins
	id := fmt.Sprintf("plugin_%04d", len(s.plugins)+1)
	s.plugins[id] = &fakePlugin{
		ID:            id,
		Name:          name,
		MarketplaceID: marketplaceID,
		OwnerUserID:   ownerUserID,
		CreatedAt:     s.tick(),
		settings:      map[string]*fakePluginInstallationSetting{},
	}
	return id
}

func (f *fakeAdminAPI) seedPluginTargetRBACGroup(id string) {
	f.plugins.rbacGroups[id] = true
}

func (f *fakeAdminAPI) setPluginSetting(pluginID, target, preference string) {
	s := f.plugins
	plugin := s.plugins[pluginID]
	now := s.tick()
	if setting, ok := plugin.settings[target]; ok {
		setting.Preference = preference
		setting.UpdatedAt = now
		return
	}
	plugin.settings[target] = &fakePluginInstallationSetting{Target: target, Preference: preference, CreatedAt: now, UpdatedAt: now}
}

// withPluginsBeta answers 404 without the beta header, as the real API does.
func (f *fakeAdminAPI) withPluginsBeta(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(strings.Split(r.Header.Get("anthropic-beta"), ","), fakePluginsBeta) {
			writeAPIError(w, http.StatusNotFound, "not_found_error", "Not found")
			return
		}
		fn(w, r)
	}
}

func (f *fakeAdminAPI) withOrganizationPlugin(fn func(http.ResponseWriter, *http.Request, *fakePlugin)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		plugin, ok := f.plugins.plugins[r.PathValue("plugin_id")]
		if !ok || plugin.OwnerUserID != "" {
			writeAPIError(w, http.StatusNotFound, "not_found_error", "Plugin not found")
			return
		}
		fn(w, r, plugin)
	}
}

func (f *fakeAdminAPI) withPluginTarget(fn func(http.ResponseWriter, *http.Request, *fakePlugin, string)) http.HandlerFunc {
	return f.withOrganizationPlugin(func(w http.ResponseWriter, r *http.Request, plugin *fakePlugin) {
		target := r.PathValue("target")
		if target != "organization" && !strings.HasPrefix(target, "rbac_group_") {
			fakeInvalid(w, "target: must be organization or an rbac_group_ ID")
			return
		}
		if strings.HasPrefix(target, "rbac_group_") && !f.plugins.rbacGroups[target] {
			writeAPIError(w, http.StatusNotFound, "not_found_error", "RBAC group not found")
			return
		}
		fn(w, r, plugin, target)
	})
}

func (f *fakeAdminAPI) withPluginMarketplace(fn func(http.ResponseWriter, *http.Request, *fakePluginMarketplace)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("marketplace_id")
		if !strings.HasPrefix(id, "marketplace_") {
			fakeInvalid(w, "marketplace_id: must start with marketplace_")
			return
		}
		marketplace, ok := f.plugins.marketplaces[id]
		if !ok {
			writeAPIError(w, http.StatusNotFound, "not_found_error", "Marketplace not found")
			return
		}
		fn(w, r, marketplace)
	}
}

func fakePluginLimit(w http.ResponseWriter, r *http.Request, maximum int) bool {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maximum {
		return fakeInvalid(w, "limit: must be between 1 and %d", maximum)
	}
	return true
}

func fakeOwner(ownerUserID string) map[string]any {
	if ownerUserID == "" {
		return map[string]any{"type": "organization"}
	}
	return map[string]any{"type": "user", "user_id": ownerUserID}
}

func fakeSettingTarget(target string) map[string]any {
	if target == "organization" {
		return map[string]any{"type": "organization"}
	}
	return map[string]any{"type": "rbac_group", "rbac_group_id": target}
}

func (f *fakeAdminAPI) encodePlugin(plugin *fakePlugin) any {
	var preference, inherited any
	if plugin.OwnerUserID == "" {
		inherited = true
		preference = "not_available"
		if marketplace, ok := f.plugins.marketplaces[plugin.MarketplaceID]; ok && marketplace.Default != "" {
			preference = marketplace.Default
		}
		if setting, ok := plugin.settings["organization"]; ok {
			preference = setting.Preference
			inherited = false
		}
	}
	return map[string]any{
		"type":           "plugin",
		"id":             plugin.ID,
		"name":           plugin.Name,
		"display_name":   strings.ToUpper(plugin.Name[:1]) + plugin.Name[1:],
		"description":    "The " + plugin.Name + " plugin.",
		"marketplace_id": plugin.MarketplaceID,
		"owner":          fakeOwner(plugin.OwnerUserID),
		"components": []any{
			map[string]any{"type": "skill", "name": plugin.Name, "description": "Does " + plugin.Name + " things."},
		},
		"content_scan":                         nil,
		"created_by":                           map[string]any{"type": "api_actor", "api_key_id": "apikey_0001"},
		"latest_version_id":                    plugin.ID + "_v1",
		"served_version_id":                    plugin.ID + "_v1",
		"manifest_version":                     "1.0.0",
		"reach":                                "contained",
		"served_version_pinned":                false,
		"organization_installation_preference": preference,
		"organization_installation_preference_inherited": inherited,
		"created_at": plugin.CreatedAt.Format(time.RFC3339),
		"updated_at": plugin.CreatedAt.Format(time.RFC3339),
	}
}

func (f *fakeAdminAPI) listPlugins(w http.ResponseWriter, r *http.Request) {
	if !fakePluginLimit(w, r, 100) {
		return
	}
	query := r.URL.Query()
	ownerType := query.Get("owner_type")
	if ownerType != "" && ownerType != "organization" && ownerType != "user" {
		fakeInvalid(w, "owner_type: must be organization or user")
		return
	}
	bounds := map[string]func(time.Time, time.Time) bool{
		"created_at[gt]":  func(c, b time.Time) bool { return c.After(b) },
		"created_at[gte]": func(c, b time.Time) bool { return !c.Before(b) },
		"created_at[lt]":  func(c, b time.Time) bool { return c.Before(b) },
		"created_at[lte]": func(c, b time.Time) bool { return !c.After(b) },
	}
	type bound struct {
		at    time.Time
		match func(time.Time, time.Time) bool
	}
	var active []bound
	for key, match := range bounds {
		raw := query.Get(key)
		if raw == "" {
			continue
		}
		at, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			fakeInvalid(w, "%s: must be an RFC 3339 timestamp", key)
			return
		}
		active = append(active, bound{at, match})
	}

	var matched []*fakePlugin
	for _, plugin := range f.plugins.plugins {
		if ownerType == "organization" && plugin.OwnerUserID != "" || ownerType == "user" && plugin.OwnerUserID == "" {
			continue
		}
		if id := query.Get("owner_user_id"); id != "" && plugin.OwnerUserID != id {
			continue
		}
		if id := query.Get("marketplace_id"); id != "" && plugin.MarketplaceID != id {
			continue
		}
		inRange := true
		for _, b := range active {
			inRange = inRange && b.match(plugin.CreatedAt, b.at)
		}
		if inRange {
			matched = append(matched, plugin)
		}
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].CreatedAt.After(matched[j].CreatedAt) })
	fakePage(w, r, matched, f.encodePlugin)
}

func encodePluginInstallationSetting(pluginID string) func(*fakePluginInstallationSetting) any {
	return func(setting *fakePluginInstallationSetting) any {
		return map[string]any{
			"type":                    "plugin_installation_setting",
			"plugin_id":               pluginID,
			"target":                  fakeSettingTarget(setting.Target),
			"installation_preference": setting.Preference,
			"created_at":              setting.CreatedAt.Format(time.RFC3339),
			"updated_at":              setting.UpdatedAt.Format(time.RFC3339),
		}
	}
}

func (f *fakeAdminAPI) listPluginInstallationSettings(w http.ResponseWriter, r *http.Request, plugin *fakePlugin) {
	if !fakePluginLimit(w, r, 100) {
		return
	}
	targetType := r.URL.Query().Get("target_type")
	if targetType != "" && targetType != "organization" && targetType != "rbac_group" {
		fakeInvalid(w, "target_type: must be organization or rbac_group")
		return
	}
	var matched []*fakePluginInstallationSetting
	for _, setting := range plugin.settings {
		if targetType == "organization" && setting.Target != "organization" || targetType == "rbac_group" && setting.Target == "organization" {
			continue
		}
		matched = append(matched, setting)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].CreatedAt.After(matched[j].CreatedAt) })
	fakePage(w, r, matched, encodePluginInstallationSetting(plugin.ID))
}

func (f *fakeAdminAPI) setPluginInstallationSetting(w http.ResponseWriter, r *http.Request, plugin *fakePlugin, target string) {
	preference, ok := decodeFakePreference(w, r, "installation_preference")
	if !ok {
		return
	}
	if setting, exists := plugin.settings[target]; !exists || setting.Preference != preference {
		f.setPluginSetting(plugin.ID, target, preference)
	}
	writeFakeJSON(w, http.StatusOK, encodePluginInstallationSetting(plugin.ID)(plugin.settings[target]))
}

func (f *fakeAdminAPI) removePluginInstallationSetting(w http.ResponseWriter, _ *http.Request, plugin *fakePlugin, target string) {
	if _, ok := plugin.settings[target]; !ok {
		writeAPIError(w, http.StatusNotFound, "not_found_error", "The target holds no installation setting of its own")
		return
	}
	delete(plugin.settings, target)
	writeFakeJSON(w, http.StatusOK, map[string]any{
		"type":      "plugin_installation_setting_deleted",
		"plugin_id": plugin.ID,
		"target":    fakeSettingTarget(target),
	})
}

func (f *fakeAdminAPI) encodePluginMarketplace(marketplace *fakePluginMarketplace) any {
	preference := "not_available"
	if marketplace.Default != "" {
		preference = marketplace.Default
	}
	var syncStatus, lastSyncEndedAt, lastSyncReadSha any
	if marketplace.Source != "manual" {
		syncStatus = "success"
		lastSyncEndedAt = marketplace.CreatedAt.Format(time.RFC3339)
		lastSyncReadSha = "9fceb02d0ae598e95dc970b74767f19372d61af8"
	}
	return map[string]any{
		"type":                            "plugin_marketplace",
		"id":                              marketplace.ID,
		"name":                            marketplace.Name,
		"owner":                           fakeOwner(marketplace.OwnerUserID),
		"source":                          marketplace.Source,
		"sync_status":                     syncStatus,
		"last_sync_ended_at":              lastSyncEndedAt,
		"last_sync_read_sha":              lastSyncReadSha,
		"default_installation_preference": preference,
		"created_at":                      marketplace.CreatedAt.Format(time.RFC3339),
	}
}

func (f *fakeAdminAPI) listPluginMarketplaces(w http.ResponseWriter, r *http.Request) {
	if !fakePluginLimit(w, r, 1000) {
		return
	}
	ownerType := r.URL.Query().Get("owner_type")
	source := r.URL.Query().Get("source")
	var matched []*fakePluginMarketplace
	for _, marketplace := range f.plugins.marketplaces {
		if ownerType == "organization" && marketplace.OwnerUserID != "" || ownerType == "user" && marketplace.OwnerUserID == "" {
			continue
		}
		if source != "" && marketplace.Source != source {
			continue
		}
		matched = append(matched, marketplace)
	}
	sort.Slice(matched, func(i, j int) bool { return matched[i].CreatedAt.After(matched[j].CreatedAt) })
	fakePage(w, r, matched, f.encodePluginMarketplace)
}

func (f *fakeAdminAPI) getPluginMarketplace(w http.ResponseWriter, _ *http.Request, marketplace *fakePluginMarketplace) {
	writeFakeJSON(w, http.StatusOK, f.encodePluginMarketplace(marketplace))
}

func (f *fakeAdminAPI) updatePluginMarketplace(w http.ResponseWriter, r *http.Request, marketplace *fakePluginMarketplace) {
	if marketplace.OwnerUserID != "" {
		writeAPIError(w, http.StatusForbidden, "permission_error", "A member's personal marketplace cannot be updated")
		return
	}
	preference, ok := decodeFakePreference(w, r, "default_installation_preference")
	if !ok {
		return
	}
	marketplace.Default = preference
	writeFakeJSON(w, http.StatusOK, f.encodePluginMarketplace(marketplace))
}

func decodeFakePreference(w http.ResponseWriter, r *http.Request, field string) (string, bool) {
	var body map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return "", fakeInvalid(w, "%s", err.Error())
	}
	var preference string
	if err := json.Unmarshal(body[field], &preference); err != nil || !slices.Contains(fakeInstallationPreferences, preference) {
		return "", fakeInvalid(w, "%s: must be one of %s", field, strings.Join(fakeInstallationPreferences, ", "))
	}
	return preference, true
}
