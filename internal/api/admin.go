package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hush.app/server/internal/db"
	"hush.app/server/internal/models"
	"hush.app/server/internal/version"

	"github.com/go-chi/chi/v5"
)

// banDisconnectGrace is the delay between sending the instance_banned WS notification
// and forcefully disconnecting the user. Gives the client time to receive the message.
const banDisconnectGrace = 500 * time.Millisecond

// RequireAdminAPIKey is middleware that enforces X-Admin-Key header authentication.
// It returns 401 when the header is missing or does not match adminAPIKey.
// This middleware is applied to all /api/admin routes.
func RequireAdminAPIKey(adminAPIKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get("X-Admin-Key")
			if key == "" || key != adminAPIKey {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or missing admin API key"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// AdminAPIRoutes returns the chi router for /api/admin.
// All routes require the X-Admin-Key header — no user auth is used.
// The admin API is the only place from which instance-level privileged operations
// are performed in the opacity model.
func AdminAPIRoutes(store db.Store, adminAPIKey string, hub GlobalBroadcaster, cache *InstanceCache) chi.Router {
	h := &adminHandler{store: store, hub: hub, cache: cache, startedAt: time.Now()}
	r := chi.NewRouter()
	r.Use(RequireAdminAPIKey(adminAPIKey))
	r.Get("/guilds", h.listGuilds)
	r.Get("/users", h.listUsers)
	r.Get("/health", h.health)
	r.Get("/config", h.getConfig)
	r.Put("/config", h.updateConfig)
	r.Get("/templates", h.listServerTemplates)
	r.Post("/templates", h.createServerTemplate)
	r.Put("/templates/{templateId}", h.updateServerTemplate)
	r.Delete("/templates/{templateId}", h.deleteServerTemplate)
	r.Post("/bans", h.instanceBan)
	r.Delete("/bans/{userId}", h.instanceUnban)
	r.Get("/audit-log", h.instanceAuditLog)
	return r
}

type adminHandler struct {
	store     db.Store
	hub       GlobalBroadcaster
	cache     *InstanceCache
	startedAt time.Time
}

// listGuilds handles GET /api/admin/guilds.
// Returns guild infrastructure metrics without guild names (blind relay privacy boundary).
func (h *adminHandler) listGuilds(w http.ResponseWriter, r *http.Request) {
	stats, err := h.store.ListGuildBillingStats(r.Context())
	if err != nil {
		slog.Error("admin listGuilds", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load guild stats"})
		return
	}
	if stats == nil {
		stats = []models.GuildBillingStats{}
	}
	writeJSON(w, http.StatusOK, stats)
}

// listUsers handles GET /api/admin/users.
// Returns user UUIDs, registration dates, instance roles, and status.
func (h *adminHandler) listUsers(w http.ResponseWriter, r *http.Request) {
	members, err := h.store.ListMembers(r.Context())
	if err != nil {
		slog.Error("admin listUsers", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list users"})
		return
	}
	if members == nil {
		members = []models.Member{}
	}
	writeJSON(w, http.StatusOK, members)
}

// health handles GET /api/admin/health.
func (h *adminHandler) health(w http.ResponseWriter, r *http.Request) {
	dbStatus := "ok"
	if err := h.store.Ping(r.Context()); err != nil {
		dbStatus = "unreachable"
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":    "ok",
		"dbStatus":  dbStatus,
		"version":   version.ServerVersion,
		"uptimeSeconds": time.Since(h.startedAt).Seconds(),
		"startedAt": h.startedAt.Format(time.RFC3339),
	})
}

// adminConfigResponse is the response for GET /api/admin/config.
type adminConfigResponse struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	IconURL              *string `json:"iconUrl"`
	RegistrationMode     string  `json:"registrationMode"`
	GuildDiscovery       string  `json:"guildDiscovery"`
	ServerCreationPolicy string  `json:"serverCreationPolicy"`
}

// getConfig handles GET /api/admin/config.
func (h *adminHandler) getConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.store.GetInstanceConfig(r.Context())
	if err != nil {
		slog.Error("admin getConfig", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load config"})
		return
	}
	writeJSON(w, http.StatusOK, adminConfigResponse{
		ID:                   cfg.ID,
		Name:                 cfg.Name,
		IconURL:              cfg.IconURL,
		RegistrationMode:     cfg.RegistrationMode,
		GuildDiscovery:       cfg.GuildDiscovery,
		ServerCreationPolicy: cfg.ServerCreationPolicy,
	})
}

// adminUpdateConfigRequest is the body for PUT /api/admin/config.
type adminUpdateConfigRequest struct {
	Name                 *string `json:"name"`
	IconURL              *string `json:"iconUrl"`
	RegistrationMode     *string `json:"registrationMode"`
	GuildDiscovery       *string `json:"guildDiscovery"`
	ServerCreationPolicy *string `json:"serverCreationPolicy"`
}

// updateConfig handles PUT /api/admin/config.
// Updates the instance config and refreshes the handshake cache.
func (h *adminHandler) updateConfig(w http.ResponseWriter, r *http.Request) {
	var req adminUpdateConfigRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		if trimmed == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name cannot be empty"})
			return
		}
		req.Name = &trimmed
	}
	if req.RegistrationMode != nil {
		switch *req.RegistrationMode {
		case "open", "invite_only", "closed":
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "registrationMode must be open, invite_only, or closed"})
			return
		}
	}
	if req.GuildDiscovery != nil {
		switch *req.GuildDiscovery {
		case "disabled", "allowed", "required":
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "guildDiscovery must be disabled, allowed, or required"})
			return
		}
	}
	if req.ServerCreationPolicy != nil {
		switch *req.ServerCreationPolicy {
		case "open", "paid", "disabled":
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "serverCreationPolicy must be open, paid, or disabled"})
			return
		}
	}

	oldCfg, _ := h.store.GetInstanceConfig(r.Context())

	if err := h.store.UpdateInstanceConfig(r.Context(), req.Name, req.IconURL, req.RegistrationMode, req.GuildDiscovery, req.ServerCreationPolicy); err != nil {
		slog.Error("admin updateConfig", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update config"})
		return
	}

	// Audit log config_change with old/new values.
	if oldCfg != nil {
		metadata := map[string]interface{}{}
		if req.Name != nil && *req.Name != oldCfg.Name {
			metadata["name"] = map[string]string{"old": oldCfg.Name, "new": *req.Name}
		}
		if req.RegistrationMode != nil && *req.RegistrationMode != oldCfg.RegistrationMode {
			metadata["registration_mode"] = map[string]string{"old": oldCfg.RegistrationMode, "new": *req.RegistrationMode}
		}
		if req.GuildDiscovery != nil && *req.GuildDiscovery != oldCfg.GuildDiscovery {
			metadata["guild_discovery"] = map[string]string{"old": oldCfg.GuildDiscovery, "new": *req.GuildDiscovery}
		}
		if req.ServerCreationPolicy != nil && *req.ServerCreationPolicy != oldCfg.ServerCreationPolicy {
			metadata["server_creation_policy"] = map[string]string{"old": oldCfg.ServerCreationPolicy, "new": *req.ServerCreationPolicy}
		}
		if len(metadata) > 0 {
			if err := h.store.InsertInstanceAuditLog(r.Context(), "admin-api", nil, "config_change", "instance config updated via admin API", metadata); err != nil {
				slog.Error("admin updateConfig: insert audit log", "err", err)
			}
		}
	}

	// Refresh handshake cache.
	newCfg, cfgErr := h.store.GetInstanceConfig(r.Context())
	if cfgErr != nil {
		slog.Warn("admin updateConfig: cache refresh failed", "err", cfgErr)
		newCfg = nil
	}
	if h.cache != nil && newCfg != nil {
		voiceKeyRotationHours, vkrhErr := h.store.GetVoiceKeyRotationHours(r.Context())
		if vkrhErr != nil {
			slog.Warn("admin updateConfig: failed to read voice_key_rotation_hours, using default", "err", vkrhErr)
			voiceKeyRotationHours = 2
		}
		h.cache.Set(newCfg.Name, newCfg.IconURL, newCfg.RegistrationMode, newCfg.GuildDiscovery, voiceKeyRotationHours, newCfg.ServerCreationPolicy)
	}

	w.WriteHeader(http.StatusNoContent)
	if h.hub != nil {
		payload := map[string]interface{}{"type": "instance_updated"}
		if newCfg != nil {
			payload["name"] = newCfg.Name
			payload["icon_url"] = newCfg.IconURL
			payload["registration_mode"] = newCfg.RegistrationMode
		}
		msg, _ := json.Marshal(payload)
		h.hub.BroadcastToAll(msg)
	}
}

// listServerTemplates handles GET /api/admin/templates.
func (h *adminHandler) listServerTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := h.store.ListServerTemplates(r.Context())
	if err != nil {
		slog.Error("admin listServerTemplates", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list templates"})
		return
	}
	writeJSON(w, http.StatusOK, templates)
}

// validChannelTypesAdmin enumerates allowed channel types in the server template (admin API).
var validChannelTypesAdmin = map[string]bool{
	"text":     true,
	"voice":    true,
	"category": true,
	"system":   true,
}

// adminServerTemplateRequest is the JSON body for POST/PUT server templates via admin API.
type adminServerTemplateRequest struct {
	Name      string                   `json:"name"`
	Channels  []models.TemplateChannel `json:"channels"`
	IsDefault bool                     `json:"isDefault"`
}

// validateAdminTemplateChannels validates channels for a server template.
func validateAdminTemplateChannels(channels []models.TemplateChannel) string {
	if len(channels) == 0 {
		return "channels must not be empty"
	}
	hasSystem := false
	for _, tc := range channels {
		if !validChannelTypesAdmin[tc.Type] {
			return "invalid channel type: " + tc.Type
		}
		if tc.Type == "voice" {
			if tc.VoiceMode == nil {
				return "voice channel must have voiceMode"
			}
			if *tc.VoiceMode != "quality" && *tc.VoiceMode != "low-latency" {
				return "voiceMode must be quality or low-latency"
			}
		} else if tc.VoiceMode != nil {
			return "only voice channels may have voiceMode"
		}
		if tc.Type == "category" && tc.ParentRef != nil {
			return "categories cannot have parentRef"
		}
		if tc.Type == "system" {
			hasSystem = true
		}
	}
	if !hasSystem {
		return "system channel is required in template"
	}
	return ""
}

// createServerTemplate handles POST /api/admin/templates.
func (h *adminHandler) createServerTemplate(w http.ResponseWriter, r *http.Request) {
	var req adminServerTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if errMsg := validateAdminTemplateChannels(req.Channels); errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}
	channelsJSON, err := json.Marshal(req.Channels)
	if err != nil {
		slog.Error("admin createServerTemplate: marshal channels", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to encode channels"})
		return
	}
	tmpl, err := h.store.CreateServerTemplate(r.Context(), strings.TrimSpace(req.Name), channelsJSON, req.IsDefault)
	if err != nil {
		slog.Error("admin createServerTemplate", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create template"})
		return
	}
	if err := h.store.InsertInstanceAuditLog(r.Context(), "admin-api", nil, "template_created", "server template created: "+tmpl.Name, nil); err != nil {
		slog.Error("admin createServerTemplate: insert audit log", "err", err)
	}
	writeJSON(w, http.StatusCreated, tmpl)
}

// updateServerTemplate handles PUT /api/admin/templates/{templateId}.
func (h *adminHandler) updateServerTemplate(w http.ResponseWriter, r *http.Request) {
	templateID := chi.URLParam(r, "templateId")
	var req adminServerTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if errMsg := validateAdminTemplateChannels(req.Channels); errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}
	channelsJSON, err := json.Marshal(req.Channels)
	if err != nil {
		slog.Error("admin updateServerTemplate: marshal channels", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to encode channels"})
		return
	}
	if err := h.store.UpdateServerTemplate(r.Context(), templateID, strings.TrimSpace(req.Name), channelsJSON, req.IsDefault); err != nil {
		slog.Error("admin updateServerTemplate", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update template"})
		return
	}
	if err := h.store.InsertInstanceAuditLog(r.Context(), "admin-api", nil, "template_updated", "server template updated: "+req.Name, nil); err != nil {
		slog.Error("admin updateServerTemplate: insert audit log", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteServerTemplate handles DELETE /api/admin/templates/{templateId}.
func (h *adminHandler) deleteServerTemplate(w http.ResponseWriter, r *http.Request) {
	templateID := chi.URLParam(r, "templateId")
	existing, err := h.store.GetServerTemplateByID(r.Context(), templateID)
	if err != nil || existing == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "template not found"})
		return
	}
	if err := h.store.DeleteServerTemplate(r.Context(), templateID); err != nil {
		slog.Error("admin deleteServerTemplate", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete template"})
		return
	}
	if err := h.store.InsertInstanceAuditLog(r.Context(), "admin-api", nil, "template_deleted", "server template deleted: "+existing.Name, nil); err != nil {
		slog.Error("admin deleteServerTemplate: insert audit log", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// instanceBan handles POST /api/admin/bans.
// Instance-level ban: revoke sessions, disconnect WS, insert ban, remove from guilds.
func (h *adminHandler) instanceBan(w http.ResponseWriter, r *http.Request) {
	var req models.InstanceBanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.UserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "userId is required"})
		return
	}
	if req.Reason == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reason is required"})
		return
	}

	var expiresAt *time.Time
	if req.ExpiresIn != nil {
		t := time.Now().Add(time.Duration(*req.ExpiresIn) * time.Second)
		expiresAt = &t
	}

	// 1. Delete sessions to prevent race condition.
	if err := h.store.DeleteSessionsByUserID(r.Context(), req.UserID); err != nil {
		slog.Warn("admin instanceBan: delete sessions (best-effort)", "user_id", req.UserID, "err", err)
	}

	// 2. Notify user via WS then disconnect.
	if h.hub != nil {
		banMsg, _ := json.Marshal(map[string]interface{}{
			"type":   "instance_banned",
			"reason": req.Reason,
		})
		h.hub.BroadcastToUser(req.UserID, banMsg)
		targetUserID := req.UserID
		time.AfterFunc(banDisconnectGrace, func() {
			h.hub.DisconnectUser(targetUserID)
		})
	}

	// 3. Insert ban record.
	_, err := h.store.InsertInstanceBan(r.Context(), req.UserID, "admin-api", req.Reason, expiresAt)
	if err != nil {
		slog.Error("admin instanceBan: insert ban", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create ban"})
		return
	}

	// 4. Remove from all guilds and broadcast member_left (silent).
	guilds, _ := h.store.ListServersForUser(r.Context(), req.UserID)
	for _, guild := range guilds {
		if err := h.store.RemoveServerMember(r.Context(), guild.ID, req.UserID); err != nil {
			slog.Error("admin instanceBan: remove guild member", "guild_id", guild.ID, "user_id", req.UserID, "err", err)
		}
		if h.hub != nil {
			msg, _ := json.Marshal(map[string]interface{}{
				"type":    "member_left",
				"user_id": req.UserID,
			})
			h.hub.BroadcastToServer(guild.ID, msg)
		}
	}

	// 5. Audit log.
	var metadata map[string]interface{}
	if req.ExpiresIn != nil {
		metadata = map[string]interface{}{"expires_in": *req.ExpiresIn}
	}
	if err := h.store.InsertInstanceAuditLog(r.Context(), "admin-api", &req.UserID, "instance_ban", req.Reason, metadata); err != nil {
		slog.Error("admin instanceBan: insert audit log", "err", err)
	}

	w.WriteHeader(http.StatusNoContent)
}

// instanceUnban handles DELETE /api/admin/bans/{userId}.
func (h *adminHandler) instanceUnban(w http.ResponseWriter, r *http.Request) {
	targetUserID := chi.URLParam(r, "userId")
	if targetUserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "userId is required"})
		return
	}
	var unbanReq struct {
		Reason string `json:"reason"`
	}
	_ = json.NewDecoder(r.Body).Decode(&unbanReq)
	reason := strings.TrimSpace(unbanReq.Reason)
	if reason == "" {
		reason = "admin api unban"
	}
	ban, err := h.store.GetActiveInstanceBan(r.Context(), targetUserID)
	if err != nil {
		slog.Error("admin instanceUnban: get active ban", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check ban status"})
		return
	}
	if ban == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active instance ban for this user"})
		return
	}
	if err := h.store.LiftInstanceBan(r.Context(), ban.ID, "admin-api"); err != nil {
		slog.Error("admin instanceUnban: lift ban", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to lift ban"})
		return
	}
	if err := h.store.InsertInstanceAuditLog(r.Context(), "admin-api", &targetUserID, "instance_unban", reason, nil); err != nil {
		slog.Error("admin instanceUnban: insert audit log", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// instanceAuditLog handles GET /api/admin/audit-log.
func (h *adminHandler) instanceAuditLog(w http.ResponseWriter, r *http.Request) {
	limit := 50
	offset := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 100 {
		limit = 100
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	var filter *db.InstanceAuditLogFilter
	action := r.URL.Query().Get("action")
	targetID := r.URL.Query().Get("target_id")
	if action != "" || targetID != "" {
		filter = &db.InstanceAuditLogFilter{
			Action:   action,
			TargetID: targetID,
		}
	}

	entries, err := h.store.ListInstanceAuditLog(r.Context(), limit, offset, filter)
	if err != nil {
		slog.Error("admin instanceAuditLog", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load audit log"})
		return
	}
	if entries == nil {
		entries = []models.InstanceAuditLogEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}
