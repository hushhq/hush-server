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

	"github.com/go-chi/chi/v5"
)

// GlobalBroadcaster is satisfied by *ws.Hub. Used for dependency injection in tests.
type GlobalBroadcaster interface {
	BroadcastToAll(message []byte)
	BroadcastToServer(serverID string, message []byte)
	BroadcastToUser(userID string, message []byte)
	DisconnectUser(userID string)
}

// roleOrder maps role names to their numeric rank for comparison.
var roleOrder = map[string]int{
	"owner":  3,
	"admin":  2,
	"mod":    1,
	"member": 0,
}

// roleAtLeast returns true if userRole is at least as privileged as required.
func roleAtLeast(userRole, required string) bool {
	return roleOrder[userRole] >= roleOrder[required]
}

// InstanceRoutes returns the router for /api/instance.
func InstanceRoutes(store db.Store, hub GlobalBroadcaster, jwtSecret string) chi.Router {
	h := &instanceHandler{store: store, hub: hub}
	r := chi.NewRouter()
	r.Use(RequireAuth(jwtSecret, store))
	r.Get("/", h.getConfig)
	r.Put("/", h.updateConfig)
	r.Get("/members", h.listMembers)
	r.Get("/users", h.searchUsers)
	r.Post("/bans", h.instanceBan)
	r.Post("/unban", h.instanceUnban)
	r.Get("/audit-log", h.instanceAuditLog)
	return r
}

type instanceHandler struct {
	store db.Store
	hub   GlobalBroadcaster
}

// instanceConfigResponse extends InstanceConfig with a bootstrapped flag and the caller's role.
type instanceConfigResponse struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	IconURL              *string `json:"iconUrl"`
	OwnerID              *string `json:"ownerId"`
	RegistrationMode     string  `json:"registrationMode"`
	ServerCreationPolicy string  `json:"serverCreationPolicy"`
	Bootstrapped         bool    `json:"bootstrapped"`
	MyRole               string  `json:"myRole"`
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
		ID:                   cfg.ID,
		Name:                 cfg.Name,
		IconURL:              cfg.IconURL,
		OwnerID:              cfg.OwnerID,
		RegistrationMode:     cfg.RegistrationMode,
		ServerCreationPolicy: cfg.ServerCreationPolicy,
		Bootstrapped:         cfg.OwnerID != nil,
		MyRole:               role,
	})
}

// updateConfigRequest is the JSON body for PUT /api/instance.
type updateConfigRequest struct {
	Name                 *string `json:"name"`
	IconURL              *string `json:"iconUrl"`
	RegistrationMode     *string `json:"registrationMode"`
	ServerCreationPolicy *string `json:"serverCreationPolicy"`
}

func (h *instanceHandler) updateConfig(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	role, err := h.store.GetUserRole(r.Context(), userID)
	if err != nil {
		slog.Error("get user role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
		return
	}
	if !roleAtLeast(role, "owner") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "owner role required"})
		return
	}
	var req updateConfigRequest
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
	if req.ServerCreationPolicy != nil {
		switch *req.ServerCreationPolicy {
		case "any_member", "admin_only":
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "serverCreationPolicy must be any_member or admin_only"})
			return
		}
	}

	// Capture old config for audit log
	oldCfg, _ := h.store.GetInstanceConfig(r.Context())

	if err := h.store.UpdateInstanceConfig(r.Context(), req.Name, req.IconURL, req.RegistrationMode, req.ServerCreationPolicy); err != nil {
		slog.Error("update instance config", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update instance config"})
		return
	}

	// Audit log config_change with old/new values
	if oldCfg != nil {
		metadata := map[string]interface{}{}
		if req.Name != nil && *req.Name != oldCfg.Name {
			metadata["name"] = map[string]string{"old": oldCfg.Name, "new": *req.Name}
		}
		if req.RegistrationMode != nil && *req.RegistrationMode != oldCfg.RegistrationMode {
			metadata["registration_mode"] = map[string]string{"old": oldCfg.RegistrationMode, "new": *req.RegistrationMode}
		}
		if req.ServerCreationPolicy != nil && *req.ServerCreationPolicy != oldCfg.ServerCreationPolicy {
			metadata["server_creation_policy"] = map[string]string{"old": oldCfg.ServerCreationPolicy, "new": *req.ServerCreationPolicy}
		}
		if len(metadata) > 0 {
			if err := h.store.InsertInstanceAuditLog(r.Context(), userID, nil, "config_change", "instance config updated", metadata); err != nil {
				slog.Error("insert instance audit log for config change", "err", err)
			}
		}
	}

	w.WriteHeader(http.StatusNoContent)
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type": "instance_updated",
		})
		h.hub.BroadcastToAll(msg)
	}
}

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
	if !roleAtLeast(role, "admin") {
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
	if !roleAtLeast(role, "admin") {
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
	if targetRole == "owner" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot ban the instance owner"})
		return
	}
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
	if !roleAtLeast(role, "admin") {
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

// instanceAuditLog handles GET /api/instance/audit-log
func (h *instanceHandler) instanceAuditLog(w http.ResponseWriter, r *http.Request) {
	actorID := userIDFromContext(r.Context())
	role, err := h.store.GetUserRole(r.Context(), actorID)
	if err != nil {
		slog.Error("get user role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
		return
	}
	if !roleAtLeast(role, "owner") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "owner role required"})
		return
	}

	// Parse query params
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
		slog.Error("list instance audit log", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load audit log"})
		return
	}
	if entries == nil {
		entries = []models.InstanceAuditLogEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}
