package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"hush.app/server/internal/db"
	"hush.app/server/internal/models"

	"github.com/go-chi/chi/v5"
)

// GlobalBroadcaster is satisfied by *ws.Hub. Used for dependency injection in tests.
type GlobalBroadcaster interface {
	BroadcastToAll(message []byte)
	BroadcastToServer(serverID string, message []byte)
	BroadcastToUser(userID string, message []byte)
	DisconnectUser(userID string)
}

// InstanceRoutes returns the router for /api/instance.
// Owner-gated and admin-gated privileged operations have been moved to AdminAPIRoutes.
// This router serves authenticated user-facing endpoints only.
func InstanceRoutes(store db.Store, hub GlobalBroadcaster, jwtSecret string, cache *InstanceCache) chi.Router {
	h := &instanceHandler{store: store, hub: hub, cache: cache}
	r := chi.NewRouter()
	r.Use(RequireAuth(jwtSecret, store))
	r.Get("/", h.getConfig)
	r.Get("/members", h.listMembers)
	r.Get("/users", h.searchUsers)
	r.Post("/bans", h.instanceBan)
	r.Post("/unban", h.instanceUnban)
	r.Get("/server-templates", h.listServerTemplates)
	return r
}

type instanceHandler struct {
	store db.Store
	hub   GlobalBroadcaster
	cache *InstanceCache
}

// instanceConfigResponse is the response for GET /api/instance.
// MyRole exposes the authenticated user's instance-level role so the client
// can conditionally show admin UI elements.
type instanceConfigResponse struct {
	ID               string  `json:"id"`
	Name             string  `json:"name"`
	IconURL          *string `json:"iconUrl"`
	RegistrationMode string  `json:"registrationMode"`
	GuildDiscovery   string  `json:"guildDiscovery"`
	MyRole           string  `json:"myRole"`
}

func (h *instanceHandler) getConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.store.GetInstanceConfig(r.Context())
	if err != nil {
		slog.Error("get instance config", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load instance config"})
		return
	}
	userID := userIDFromContext(r.Context())
	role, err := h.store.GetUserRole(r.Context(), userID)
	if err != nil {
		slog.Error("get user role for instance config", "err", err)
		role = "member"
	}
	writeJSON(w, http.StatusOK, instanceConfigResponse{
		ID:               cfg.ID,
		Name:             cfg.Name,
		IconURL:          cfg.IconURL,
		RegistrationMode: cfg.RegistrationMode,
		GuildDiscovery:   cfg.GuildDiscovery,
		MyRole:           role,
	})
}

// updateConfig is no longer on InstanceRoutes — it has moved to AdminAPIRoutes (PUT /api/admin/config).
// This stub is intentionally absent; see admin.go.

func (h *instanceHandler) listMembers(w http.ResponseWriter, r *http.Request) {
	members, err := h.store.ListMembers(r.Context())
	if err != nil {
		slog.Error("list members", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list members"})
		return
	}
	if members == nil {
		members = []models.Member{}
	}
	writeJSON(w, http.StatusOK, members)
}

// searchUsers handles GET /api/instance/users?q=prefix
func (h *instanceHandler) searchUsers(w http.ResponseWriter, r *http.Request) {
	actorID := userIDFromContext(r.Context())
	role, err := h.store.GetUserRole(r.Context(), actorID)
	if err != nil {
		slog.Error("get user role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
		return
	}
	if role != "admin" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role required"})
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query parameter q is required"})
		return
	}
	results, err := h.store.SearchUsers(r.Context(), q, 25)
	if err != nil {
		slog.Error("search users", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "search failed"})
		return
	}
	if results == nil {
		results = []models.UserSearchResult{}
	}
	writeJSON(w, http.StatusOK, results)
}

// instanceBan handles POST /api/instance/bans
func (h *instanceHandler) instanceBan(w http.ResponseWriter, r *http.Request) {
	actorID := userIDFromContext(r.Context())
	role, err := h.store.GetUserRole(r.Context(), actorID)
	if err != nil {
		slog.Error("get user role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
		return
	}
	if role != "admin" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role required"})
		return
	}
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
	if req.UserID == actorID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot ban yourself"})
		return
	}
	// Check target role — cannot ban owner; admin cannot ban another admin
	targetRole, err := h.store.GetUserRole(r.Context(), req.UserID)
	if err != nil {
		slog.Error("get target user role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify target role"})
		return
	}
	// Admin cannot ban other admins — only the instance operator (API key level) can.
	if role == "admin" && targetRole == "admin" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin cannot ban another admin"})
		return
	}

	// Calculate expiry
	var expiresAt *time.Time
	if req.ExpiresIn != nil {
		t := time.Now().Add(time.Duration(*req.ExpiresIn) * time.Second)
		expiresAt = &t
	}

	// CRITICAL ORDER: revoke sessions first to prevent race condition
	// 1. Delete sessions (prevents new guild joins during cascade)
	_ = h.store.DeleteSessionsByUserID(r.Context(), req.UserID)

	// 2. Notify user via WS, then disconnect after flush delay
	if h.hub != nil {
		banMsg, _ := json.Marshal(map[string]interface{}{
			"type":   "instance_banned",
			"reason": req.Reason,
		})
		h.hub.BroadcastToUser(req.UserID, banMsg)
		targetUserID := req.UserID
		time.AfterFunc(500*time.Millisecond, func() {
			h.hub.DisconnectUser(targetUserID)
		})
	}

	// 3. Insert instance ban record
	_, err = h.store.InsertInstanceBan(r.Context(), req.UserID, actorID, req.Reason, expiresAt)
	if err != nil {
		slog.Error("insert instance ban", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create ban"})
		return
	}

	// 4. Get all guilds and remove from each + broadcast member_left (silent)
	guilds, _ := h.store.ListServersForUser(r.Context(), req.UserID)
	for _, guild := range guilds {
		_ = h.store.RemoveServerMember(r.Context(), guild.ID, req.UserID)
		if h.hub != nil {
			msg, _ := json.Marshal(map[string]interface{}{
				"type":    "member_left",
				"user_id": req.UserID,
			})
			h.hub.BroadcastToServer(guild.ID, msg)
		}
	}

	// 5. Audit log
	var metadata map[string]interface{}
	if req.ExpiresIn != nil {
		metadata = map[string]interface{}{"expires_in": *req.ExpiresIn}
	}
	if err := h.store.InsertInstanceAuditLog(r.Context(), actorID, &req.UserID, "instance_ban", req.Reason, metadata); err != nil {
		slog.Error("insert instance audit log for ban", "err", err)
	}

	w.WriteHeader(http.StatusNoContent)
}

// instanceUnban handles POST /api/instance/unban
func (h *instanceHandler) instanceUnban(w http.ResponseWriter, r *http.Request) {
	actorID := userIDFromContext(r.Context())
	role, err := h.store.GetUserRole(r.Context(), actorID)
	if err != nil {
		slog.Error("get user role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
		return
	}
	if role != "admin" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role required"})
		return
	}
	var req models.InstanceUnbanRequest
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
	ban, err := h.store.GetActiveInstanceBan(r.Context(), req.UserID)
	if err != nil {
		slog.Error("get active instance ban", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check ban status"})
		return
	}
	if ban == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active instance ban for this user"})
		return
	}
	if err := h.store.LiftInstanceBan(r.Context(), ban.ID, actorID); err != nil {
		slog.Error("lift instance ban", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to lift ban"})
		return
	}
	if err := h.store.InsertInstanceAuditLog(r.Context(), actorID, &req.UserID, "instance_unban", req.Reason, nil); err != nil {
		slog.Error("insert instance audit log for unban", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// instanceAuditLog moved to AdminAPIRoutes (GET /api/admin/audit-log). See admin.go.

// listServerTemplates handles GET /api/instance/server-templates.
// Available to all authenticated users (needed for template picker in guild creation).
func (h *instanceHandler) listServerTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := h.store.ListServerTemplates(r.Context())
	if err != nil {
		slog.Error("list server templates", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list templates"})
		return
	}
	writeJSON(w, http.StatusOK, templates)
}

// Server template CRUD (create/update/delete) moved to AdminAPIRoutes. See admin.go.
// listServerTemplates stays here — it's available to all authenticated users for the template picker.
