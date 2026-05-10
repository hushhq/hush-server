package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/livekit"
	"github.com/hushhq/hush-server/internal/models"
	"github.com/hushhq/hush-server/internal/version"

	"github.com/go-chi/chi/v5"
)

// banDisconnectGrace is the delay between sending the instance_banned WS notification
// and forcefully disconnecting the user. Gives the client time to receive the message.
const banDisconnectGrace = 500 * time.Millisecond

type adminStore interface {
	Ping(ctx context.Context) error
	CountInstanceAdmins(ctx context.Context) (int, error)
	CreateInstanceAdmin(ctx context.Context, username string, email *string, passwordHash, role string) (*models.InstanceAdmin, error)
	GetInstanceAdminByUsername(ctx context.Context, username string) (*models.InstanceAdmin, error)
	GetInstanceAdminByID(ctx context.Context, id string) (*models.InstanceAdmin, error)
	ListInstanceAdmins(ctx context.Context) ([]models.InstanceAdmin, error)
	UpdateInstanceAdmin(ctx context.Context, id string, email *string, role string, isActive bool) (*models.InstanceAdmin, error)
	UpdateInstanceAdminPassword(ctx context.Context, id, passwordHash string) error
	TouchInstanceAdminLastLogin(ctx context.Context, id string, loginAt time.Time) error
	CreateInstanceAdminSession(ctx context.Context, sessionID, adminID, tokenHash string, expiresAt time.Time, createdIP, userAgent *string) (*models.InstanceAdminSession, error)
	GetInstanceAdminSessionByTokenHash(ctx context.Context, tokenHash string) (*models.InstanceAdminSession, error)
	DeleteInstanceAdminSessionByID(ctx context.Context, sessionID string) error
	UpdateInstanceAdminSessionLastSeen(ctx context.Context, sessionID string, seenAt time.Time) error
	GetInstanceServiceIdentity(ctx context.Context) (*models.InstanceServiceIdentity, error)
	UpsertInstanceServiceIdentity(ctx context.Context, username string, publicKey, wrappedPrivateKey []byte, wrappingKeyVersion string) (*models.InstanceServiceIdentity, error)
	ListGuildBillingStats(ctx context.Context) ([]models.GuildBillingStats, error)
	ListMembers(ctx context.Context) ([]models.Member, error)
	GetInstanceConfig(ctx context.Context) (*models.InstanceConfig, error)
	UpdateInstanceConfig(ctx context.Context, name *string, iconURL *string, registrationMode *string, guildDiscovery *string, serverCreationPolicy *string, maxServersPerUser *int, maxMembersPerServer *int, maxRegisteredUsers *int, screenShareResolutionCap *string, maxAttachmentBytes *int64, maxGuildAttachmentStorageBytes *int64, messageRetentionDays *int) error
	GetVoiceKeyRotationHours(ctx context.Context) (int, error)
	ListServerTemplates(ctx context.Context) ([]models.ServerTemplate, error)
	GetServerTemplateByID(ctx context.Context, id string) (*models.ServerTemplate, error)
	CreateServerTemplate(ctx context.Context, name string, channels json.RawMessage, isDefault bool) (*models.ServerTemplate, error)
	UpdateServerTemplate(ctx context.Context, id string, name string, channels json.RawMessage, isDefault bool) error
	DeleteServerTemplate(ctx context.Context, id string) error
	InsertInstanceBanByAdmin(ctx context.Context, userID, actorAdminID, reason string, expiresAt *time.Time) (*models.InstanceBan, error)
	GetActiveInstanceBan(ctx context.Context, userID string) (*models.InstanceBan, error)
	LiftInstanceBanByAdmin(ctx context.Context, banID, liftedByAdminID string) error
	ListServersForUser(ctx context.Context, userID string) ([]models.Server, error)
	ListChannels(ctx context.Context, serverID string) ([]models.Channel, error)
	RemoveServerMember(ctx context.Context, serverID, userID string) error
	DeleteSessionsByUserID(ctx context.Context, userID string) error
	ListInstanceAuditLog(ctx context.Context, limit, offset int, filter *db.InstanceAuditLogFilter) ([]models.InstanceAuditLogEntry, error)
	GetServerByID(ctx context.Context, serverID string) (*models.Server, error)
	UpdateServerMemberCapOverride(ctx context.Context, serverID string, cap *int) error
}

// AdminAPIRoutes returns the chi router for /api/admin.
// Bootstrap and session routes are public; all resource routes use local admin sessions.
//
// httpMetrics and hubStats are optional operator telemetry hooks consumed by
// the /metrics route; they may be nil in tests where the metrics surface is
// not under exercise. startedAt overrides the handler's process-start time
// when non-zero so the uptime-bearing endpoints stay consistent across the
// admin and metrics surfaces wired by main.go.
func AdminAPIRoutes(
	store adminStore,
	bootstrapSecret string,
	sessionTTL time.Duration,
	secureCookies bool,
	serviceIdentityMasterKey string,
	hub GlobalBroadcaster,
	cache *InstanceCache,
	roomService livekit.RoomService,
	httpMetrics *HTTPMetrics,
	hubStats HubStatsProvider,
	startedAt time.Time,
) chi.Router {
	if startedAt.IsZero() {
		startedAt = time.Now()
	}
	h := &adminHandler{
		store:                    store,
		hub:                      hub,
		cache:                    cache,
		roomService:              roomService,
		startedAt:                startedAt,
		bootstrapSecret:          bootstrapSecret,
		sessionTTL:               sessionTTL,
		secureCookies:            secureCookies,
		serviceIdentityMasterKey: serviceIdentityMasterKey,
		httpMetrics:              httpMetrics,
		hubStats:                 hubStats,
	}
	r := chi.NewRouter()
	r.Post("/bootstrap/status", h.bootstrapStatus)
	r.Post("/bootstrap/claim", h.bootstrapClaim)
	r.Post("/session/login", h.login)
	r.With(RequireAdminSession(store), RequireAdminOrigin()).Post("/session/logout", h.logout)
	r.With(RequireAdminSession(store)).Get("/session/me", h.me)
	r.With(RequireAdminSession(store), RequireAdminOrigin()).Post("/session/change-password", h.changePassword)

	r.Group(func(protected chi.Router) {
		protected.Use(RequireAdminSession(store))
		protected.Use(RequireAdminOrigin())
		protected.Get("/guilds", h.listGuilds)
		protected.Get("/users", h.listUsers)
		protected.Get("/health", h.health)
		protected.Get("/metrics", h.metrics)
		protected.Get("/config", h.getConfig)
		protected.Put("/config", h.updateConfig)
		protected.Get("/templates", h.listServerTemplates)
		protected.Post("/templates", h.createServerTemplate)
		protected.Put("/templates/{templateId}", h.updateServerTemplate)
		protected.Delete("/templates/{templateId}", h.deleteServerTemplate)
		protected.Post("/bans", h.instanceBan)
		protected.Delete("/bans/{userId}", h.instanceUnban)
		protected.Get("/audit-log", h.instanceAuditLog)
		protected.Put("/guilds/{serverId}/member-cap", h.setServerMemberCap)
		protected.Get("/service-identity", h.getServiceIdentity)
		protected.With(RequireAdminRole("owner")).Get("/admins", h.listAdmins)
		protected.With(RequireAdminRole("owner")).Post("/admins", h.createAdminAccount)
		protected.With(RequireAdminRole("owner")).Patch("/admins/{adminId}", h.patchAdminAccount)
		protected.With(RequireAdminRole("owner")).Post("/admins/{adminId}/reset-password", h.resetAdminPassword)
		protected.With(RequireAdminRole("owner")).Post("/service-identity/provision", h.provisionServiceIdentity)
	})
	return r
}

type adminHandler struct {
	store                    adminStore
	hub                      GlobalBroadcaster
	cache                    *InstanceCache
	roomService              livekit.RoomService
	startedAt                time.Time
	bootstrapSecret          string
	sessionTTL               time.Duration
	secureCookies            bool
	serviceIdentityMasterKey string
	httpMetrics              *HTTPMetrics
	hubStats                 HubStatsProvider
}

// instanceBanEvictionTimeout caps each per-channel RemoveParticipant
// call invoked during an instance-wide ban. The instance ban path
// can fan out across every voice channel of every guild the user
// belongs to, so we cap each call individually rather than the
// outer loop to keep one slow channel from starving the rest.
const instanceBanEvictionTimeout = 4 * time.Second

// evictUserFromAllVoice removes the target user from every voice
// channel of every guild they belong to. Best-effort: the database
// state has already been written before this runs and is the
// source of truth, so a LiveKit outage cannot block the ban. Each
// failure is logged for operator triage.
func (h *adminHandler) evictUserFromAllVoice(ctx context.Context, userID string, guilds []models.Server) {
	if h.roomService == nil {
		return
	}
	for _, guild := range guilds {
		channels, err := h.store.ListChannels(ctx, guild.ID)
		if err != nil {
			slog.Error("instance ban eviction: list channels",
				"guild_id", guild.ID, "user_id", userID, "err", err)
			continue
		}
		for _, ch := range channels {
			if ch.Type != "voice" {
				continue
			}
			evictCtx, cancel := context.WithTimeout(ctx, instanceBanEvictionTimeout)
			room := "channel-" + ch.ID
			if err := h.roomService.RemoveParticipant(evictCtx, room, userID); err != nil {
				slog.Error("instance ban eviction: remove participant",
					"room", room, "user_id", userID, "err", err)
			}
			cancel()
		}
	}
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
		"status":        "ok",
		"dbStatus":      dbStatus,
		"version":       version.ServerVersion,
		"uptimeSeconds": time.Since(h.startedAt).Seconds(),
		"startedAt":     h.startedAt.Format(time.RFC3339),
	})
}

// adminConfigResponse is the response for GET /api/admin/config.
type adminConfigResponse struct {
	ID                             string  `json:"id"`
	Name                           string  `json:"name"`
	IconURL                        *string `json:"iconUrl"`
	RegistrationMode               string  `json:"registrationMode"`
	GuildDiscovery                 string  `json:"guildDiscovery"`
	ServerCreationPolicy           string  `json:"serverCreationPolicy"`
	MaxServersPerUser              *int    `json:"maxServersPerUser,omitempty"`
	MaxMembersPerServer            *int    `json:"maxMembersPerServer,omitempty"`
	MaxRegisteredUsers             *int    `json:"maxRegisteredUsers,omitempty"`
	ScreenShareResolutionCap       string  `json:"screenShareResolutionCap"`
	MaxAttachmentBytes             int64   `json:"maxAttachmentBytes"`
	MaxGuildAttachmentStorageBytes *int64  `json:"maxGuildAttachmentStorageBytes,omitempty"`
	MessageRetentionDays           int     `json:"messageRetentionDays"`
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
		ID:                             cfg.ID,
		Name:                           cfg.Name,
		IconURL:                        cfg.IconURL,
		RegistrationMode:               cfg.RegistrationMode,
		GuildDiscovery:                 cfg.GuildDiscovery,
		ServerCreationPolicy:           cfg.ServerCreationPolicy,
		MaxServersPerUser:              cfg.MaxServersPerUser,
		MaxMembersPerServer:            cfg.MaxMembersPerServer,
		MaxRegisteredUsers:             cfg.MaxRegisteredUsers,
		ScreenShareResolutionCap:       cfg.ScreenShareResolutionCap,
		MaxAttachmentBytes:             cfg.MaxAttachmentBytes,
		MaxGuildAttachmentStorageBytes: cfg.MaxGuildAttachmentStorageBytes,
		MessageRetentionDays:           cfg.MessageRetentionDays,
	})
}

// adminUpdateConfigRequest is the body for PUT /api/admin/config.
type adminUpdateConfigRequest struct {
	Name                           *string `json:"name"`
	IconURL                        *string `json:"iconUrl"`
	RegistrationMode               *string `json:"registrationMode"`
	GuildDiscovery                 *string `json:"guildDiscovery"`
	ServerCreationPolicy           *string `json:"serverCreationPolicy"`
	MaxServersPerUser              *int    `json:"maxServersPerUser,omitempty"`
	MaxMembersPerServer            *int    `json:"maxMembersPerServer,omitempty"`
	MaxRegisteredUsers             *int    `json:"maxRegisteredUsers,omitempty"`
	ScreenShareResolutionCap       *string `json:"screenShareResolutionCap,omitempty"`
	MaxAttachmentBytes             *int64  `json:"maxAttachmentBytes,omitempty"`
	MaxGuildAttachmentStorageBytes *int64  `json:"maxGuildAttachmentStorageBytes,omitempty"`
	MessageRetentionDays           *int    `json:"messageRetentionDays,omitempty"`
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
	// Capability limits: 0 = remove limit (set to NULL), >= 1 = enforce limit, < 0 = invalid.
	if req.MaxServersPerUser != nil && *req.MaxServersPerUser < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "maxServersPerUser must be 0 (no limit) or at least 1"})
		return
	}
	if req.MaxMembersPerServer != nil && *req.MaxMembersPerServer < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "maxMembersPerServer must be 0 (no limit) or at least 1"})
		return
	}
	if req.MaxRegisteredUsers != nil && *req.MaxRegisteredUsers < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "maxRegisteredUsers must be 0 (no limit) or at least 1"})
		return
	}
	if req.ScreenShareResolutionCap != nil {
		switch *req.ScreenShareResolutionCap {
		case "1080p", "720p":
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "screenShareResolutionCap must be 1080p or 720p"})
			return
		}
	}
	if req.MaxAttachmentBytes != nil && *req.MaxAttachmentBytes < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "maxAttachmentBytes must be at least 1"})
		return
	}
	if req.MaxGuildAttachmentStorageBytes != nil && *req.MaxGuildAttachmentStorageBytes < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "maxGuildAttachmentStorageBytes must be 0 (no limit) or at least 1"})
		return
	}
	if req.MessageRetentionDays != nil && *req.MessageRetentionDays < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "messageRetentionDays must be at least 1"})
		return
	}

	if err := h.store.UpdateInstanceConfig(r.Context(), req.Name, req.IconURL, req.RegistrationMode, req.GuildDiscovery, req.ServerCreationPolicy, req.MaxServersPerUser, req.MaxMembersPerServer, req.MaxRegisteredUsers, req.ScreenShareResolutionCap, req.MaxAttachmentBytes, req.MaxGuildAttachmentStorageBytes, req.MessageRetentionDays); err != nil {
		slog.Error("admin updateConfig", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update config"})
		return
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
		h.cache.Set(newCfg.Name, newCfg.IconURL, newCfg.RegistrationMode, newCfg.GuildDiscovery, voiceKeyRotationHours, newCfg.ServerCreationPolicy, newCfg.ScreenShareResolutionCap)
		h.cache.SetAttachmentPolicy(newCfg.MaxAttachmentBytes)
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
	_, err := h.store.InsertInstanceBanByAdmin(r.Context(), req.UserID, adminIDFromContext(r.Context()), req.Reason, expiresAt)
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

	// 5. Evict the banned user from every voice channel they were
	//    in. Best-effort eventual consistency; see ans17.md.
	h.evictUserFromAllVoice(r.Context(), req.UserID, guilds)

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
	if err := h.store.LiftInstanceBanByAdmin(r.Context(), ban.ID, adminIDFromContext(r.Context())); err != nil {
		slog.Error("admin instanceUnban: lift ban", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to lift ban"})
		return
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

// setServerMemberCap handles PUT /api/admin/guilds/{serverId}/member-cap.
// Sets or clears the per-server member cap override.
// Body: { "memberCapOverride": 100 } to set, { "memberCapOverride": 0 } to clear (inherit instance default).
func (h *adminHandler) setServerMemberCap(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	var req struct {
		MemberCapOverride *int `json:"memberCapOverride"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.MemberCapOverride == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "memberCapOverride is required (0 to clear, >= 1 to set)"})
		return
	}
	if *req.MemberCapOverride < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "memberCapOverride must be 0 (clear) or >= 1"})
		return
	}
	srv, err := h.store.GetServerByID(r.Context(), serverID)
	if err != nil || srv == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "server not found"})
		return
	}
	var capValue *int
	if *req.MemberCapOverride > 0 {
		capValue = req.MemberCapOverride
	}
	// capValue is nil when 0 → clears the override
	if err := h.store.UpdateServerMemberCapOverride(r.Context(), serverID, capValue); err != nil {
		slog.Error("admin setServerMemberCap", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update member cap"})
		return
	}
	cfg, _ := h.store.GetInstanceConfig(r.Context())
	effectiveCap := effectiveMemberCap(srv, cfg)
	if capValue != nil {
		effectiveCap = capValue
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"serverId":          serverID,
		"memberCapOverride": capValue,
		"effectiveCap":      effectiveCap,
	})
}
