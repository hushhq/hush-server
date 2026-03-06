package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"hush.app/server/internal/db"
	"hush.app/server/internal/models"

	"github.com/go-chi/chi/v5"
)

// ServerRoutes mounts guild CRUD, member management, and nested sub-routes
// (channels, guild invites, moderation). Auth is applied at the top level;
// RequireGuildMember is applied to all /{serverId} sub-routes.
func ServerRoutes(store db.Store, hub GlobalBroadcaster, jwtSecret string) chi.Router {
	h := &serversHandler{store: store, hub: hub}
	r := chi.NewRouter()
	r.Use(RequireAuth(jwtSecret, store))
	r.Post("/", h.createServer)
	r.Get("/", h.listMyServers)
	r.Route("/{serverId}", func(r chi.Router) {
		r.Use(RequireGuildMember(store))
		r.Get("/", h.getServer)
		r.Delete("/", h.deleteServer)
		r.Get("/members", h.listMembers)
		r.Put("/members/{userId}/role", h.changeRole)
		r.Post("/leave", h.leaveServer)
		r.Mount("/channels", ChannelRoutes(store, hub))
		r.Mount("/invites", GuildInviteRoutes(store))
		r.Mount("/moderation", ModerationRoutes(store, hub))
		r.Mount("/system-messages", SystemMessagesRoutes(store))
	})
	return r
}

// AdminRoutes mounts instance-operator endpoints (billing stats).
// Requires authentication; instance-owner check is performed inside handlers.
func AdminRoutes(store db.Store, jwtSecret string) chi.Router {
	h := &serversHandler{store: store}
	r := chi.NewRouter()
	r.Use(RequireAuth(jwtSecret, store))
	r.Get("/guilds", h.listGuildBillingStats)
	return r
}

type serversHandler struct {
	store db.Store
	hub   GlobalBroadcaster
}

// createServer handles POST /api/servers.
// Enforces server_creation_policy. The creator is auto-added as guild owner.
func (h *serversHandler) createServer(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	// Enforce server_creation_policy.
	cfg, err := h.store.GetInstanceConfig(r.Context())
	if err != nil {
		slog.Error("createServer: get instance config", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load instance config"})
		return
	}
	if cfg.ServerCreationPolicy == "admin_only" {
		instanceRole, err := h.store.GetUserRole(r.Context(), userID)
		if err != nil {
			slog.Error("createServer: get user role", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
			return
		}
		if !roleAtLeast(instanceRole, "admin") {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "server creation restricted to instance admins"})
			return
		}
	}
	var req models.CreateServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
		return
	}
	if len(req.Name) > maxNameLength {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name exceeds maximum length"})
		return
	}
	server, err := h.store.CreateServer(r.Context(), req.Name, userID)
	if err != nil {
		slog.Error("createServer: create server", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create server"})
		return
	}
	if err := h.store.AddServerMember(r.Context(), server.ID, userID, "owner"); err != nil {
		slog.Error("createServer: add server member", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to add creator as guild owner"})
		return
	}
	// Create #system channel for the new guild. Log error but don't fail — guild is still usable.
	if _, err := h.store.CreateChannel(r.Context(), server.ID, "system", "system", nil, nil, -1); err != nil {
		slog.Error("createServer: create system channel", "err", err)
	}
	writeJSON(w, http.StatusCreated, server)
}

// listMyServers handles GET /api/servers — returns guilds the caller belongs to.
func (h *serversHandler) listMyServers(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	servers, err := h.store.ListServersForUser(r.Context(), userID)
	if err != nil {
		slog.Error("listMyServers", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list servers"})
		return
	}
	if servers == nil {
		servers = []models.Server{}
	}
	writeJSON(w, http.StatusOK, servers)
}

// getServer handles GET /api/servers/{serverId}.
func (h *serversHandler) getServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	server, err := h.store.GetServerByID(r.Context(), serverID)
	if err != nil {
		slog.Error("getServer", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get server"})
		return
	}
	if server == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "server not found"})
		return
	}
	writeJSON(w, http.StatusOK, server)
}

// deleteServer handles DELETE /api/servers/{serverId}.
// Restricted to the guild owner only.
func (h *serversHandler) deleteServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	role := guildRoleFromContext(r.Context())
	if role != "owner" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "only the guild owner can delete the server"})
		return
	}
	if err := h.store.DeleteServer(r.Context(), serverID); err != nil {
		slog.Error("deleteServer", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete server"})
		return
	}
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":      "server_deleted",
			"server_id": serverID,
		})
		h.hub.BroadcastToServer(serverID, msg)
	}
	w.WriteHeader(http.StatusNoContent)
}

// listMembers handles GET /api/servers/{serverId}/members.
func (h *serversHandler) listMembers(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	members, err := h.store.ListServerMembers(r.Context(), serverID)
	if err != nil {
		slog.Error("listMembers", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list members"})
		return
	}
	if members == nil {
		members = []models.ServerMemberWithUser{}
	}
	writeJSON(w, http.StatusOK, members)
}

// changeRoleRequest is the JSON body for PUT /api/servers/{serverId}/members/{userId}/role.
type changeRoleRequest struct {
	NewRole string `json:"newRole"`
}

// changeRole handles PUT /api/servers/{serverId}/members/{userId}/role.
// Requires admin+ guild role. Actor must outrank the target's current and new role.
func (h *serversHandler) changeRole(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	targetUserID := chi.URLParam(r, "userId")
	actorID := userIDFromContext(r.Context())
	actorRole := guildRoleFromContext(r.Context())

	if targetUserID == actorID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot change your own role"})
		return
	}
	if !roleAtLeast(actorRole, "admin") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role or higher required"})
		return
	}
	var req changeRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	switch req.NewRole {
	case "member", "mod", "admin":
		// valid
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "newRole must be member, mod, or admin"})
		return
	}
	targetRole, err := h.store.GetServerMemberRole(r.Context(), serverID, targetUserID)
	if err != nil || targetRole == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "target user not found in this guild"})
		return
	}
	if roleOrder[actorRole] <= roleOrder[targetRole] {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot modify a member with equal or higher role"})
		return
	}
	if roleOrder[actorRole] <= roleOrder[req.NewRole] {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot assign a role equal or higher than your own"})
		return
	}
	if err := h.store.UpdateServerMemberRole(r.Context(), serverID, targetUserID, req.NewRole); err != nil {
		slog.Error("changeRole: update member role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update role"})
		return
	}
	metadata := map[string]interface{}{
		"old_role": targetRole,
		"new_role": req.NewRole,
	}
	if err := h.store.InsertAuditLog(r.Context(), serverID, actorID, &targetUserID, "role_change", "role changed via guild management", metadata); err != nil {
		slog.Error("changeRole: insert audit log", "err", err)
	}
	EmitSystemMessage(r.Context(), h.store, h.hub, serverID, "role_changed", actorID, &targetUserID, "role changed", metadata)
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":      "member_role_changed",
			"server_id": serverID,
			"user_id":   targetUserID,
			"new_role":  req.NewRole,
		})
		h.hub.BroadcastToServer(serverID, msg)
	}
	w.WriteHeader(http.StatusNoContent)
}

// leaveServer handles POST /api/servers/{serverId}/leave.
// Removes the caller from the guild. Guild owners cannot leave.
func (h *serversHandler) leaveServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	actorID := userIDFromContext(r.Context())
	if actorID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	role := guildRoleFromContext(r.Context())
	if role == "owner" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "guild owner cannot leave; transfer ownership first"})
		return
	}
	if err := h.store.RemoveServerMember(r.Context(), serverID, actorID); err != nil {
		slog.Error("leaveServer: remove member", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to leave guild"})
		return
	}
	// Broadcast member_left.
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":      "member_left",
			"server_id": serverID,
			"user_id":   actorID,
		})
		h.hub.BroadcastToServer(serverID, msg)
	}
	// Emit system message.
	EmitSystemMessage(r.Context(), h.store, h.hub, serverID, "member_left", actorID, nil, "", nil)
	w.WriteHeader(http.StatusNoContent)
}

// listGuildBillingStats handles GET /api/admin/guilds.
// Instance owner only — returns exactly 5 fields per guild (privacy boundary).
func (h *serversHandler) listGuildBillingStats(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	instanceRole, err := h.store.GetUserRole(r.Context(), userID)
	if err != nil {
		slog.Error("listGuildBillingStats: get user role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
		return
	}
	if instanceRole != "owner" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "instance owner required"})
		return
	}
	stats, err := h.store.ListGuildBillingStats(r.Context())
	if err != nil {
		slog.Error("listGuildBillingStats", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load billing stats"})
		return
	}
	if stats == nil {
		stats = []models.GuildBillingStats{}
	}
	writeJSON(w, http.StatusOK, stats)
}
