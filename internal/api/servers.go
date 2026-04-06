package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/models"

	"github.com/go-chi/chi/v5"
)

// effectiveMemberCap returns the active member cap for a server:
// server-specific override if set, otherwise instance default, otherwise nil (unlimited).
func effectiveMemberCap(server *models.Server, cfg *models.InstanceConfig) *int {
	if server != nil && server.MemberCapOverride != nil {
		return server.MemberCapOverride
	}
	if cfg != nil && cfg.MaxMembersPerServer != nil {
		return cfg.MaxMembersPerServer
	}
	return nil
}

// fallbackTemplate returns the built-in default channel set used when no
// server template exists in the database.
func fallbackTemplate() []models.TemplateChannel {
	return []models.TemplateChannel{
		{Name: "system", Type: "system", Position: -1},
		{Name: "general", Type: "text", Position: 0},
		{Name: "General", Type: "voice", Position: 1},
	}
}

// ServerRoutes mounts guild CRUD, member management, and nested sub-routes
// (channels, guild invites, moderation). Auth is applied at the top level;
// RequireGuildMember is applied to all member-only /{serverId} sub-routes.
// The /join endpoint is intentionally outside RequireGuildMember - the user
// is not yet a member when they hit that route.
func ServerRoutes(store db.Store, hub GlobalBroadcaster, jwtSecret string) chi.Router {
	h := &serversHandler{store: store, hub: hub}
	r := chi.NewRouter()
	r.Use(RequireAuth(jwtSecret, store))
	r.Post("/", h.createServer)
	r.Get("/", h.listMyServers)
	r.Route("/{serverId}", func(r chi.Router) {
		// Join is outside RequireGuildMember - caller is not a member yet.
		r.Post("/join", h.joinServer)
		// All other /{serverId} routes require guild membership.
		r.Group(func(r chi.Router) {
			r.Use(RequireGuildMember(store))
			r.Get("/", h.getServer)
			r.Put("/", h.updateServer)
			r.Delete("/", h.deleteServer)
			r.Get("/members", h.listMembers)
			r.Put("/members/{userId}/role", h.changeRole)
			r.Post("/leave", h.leaveServer)
			r.Mount("/channels", ChannelRoutes(store, hub))
			r.Mount("/invites", GuildInviteRoutes(store))
			r.Mount("/moderation", ModerationRoutes(store, hub))
			r.Mount("/system-messages", SystemMessagesRoutes(store))
		})
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
// Guild creation is gated by the instance server_creation_policy (open/paid/disabled).
// Guild-level access control is additionally handled by access_policy and discoverable
// flags on the guild itself.
func (h *serversHandler) createServer(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	cfg, err := h.store.GetInstanceConfig(r.Context())
	if err != nil {
		slog.Error("createServer: fetch instance config", "err", err)
		writeJSON(w, http.StatusInternalServerError,
			map[string]string{"error": "failed to verify creation policy"})
		return
	}
	switch cfg.ServerCreationPolicy {
	case "disabled":
		writeJSON(w, http.StatusForbidden,
			map[string]string{"error": "server creation is disabled on this instance"})
		return
	case "paid":
		writeJSON(w, http.StatusForbidden,
			map[string]string{"error": "This instance requires a subscription to create servers."})
		return
		// "open": fall through - any authenticated user can create.
	}
	// Check per-user server creation limit.
	if cfg.MaxServersPerUser != nil {
		owned, err := h.store.CountOwnedServers(r.Context(), userID)
		if err != nil {
			slog.Error("createServer: count owned servers", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check server limits"})
			return
		}
		if owned >= *cfg.MaxServersPerUser {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "you have reached the maximum number of servers you can create"})
			return
		}
	}
	var req models.CreateServerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	// Plaintext name fallback: when MLS is not bootstrapped, clients send Name
	// instead of EncryptedMetadata. Wrap it as a JSON blob for the metadata column.
	if len(req.EncryptedMetadata) == 0 && req.Name != "" {
		req.EncryptedMetadata = []byte(`{"n":"` + req.Name + `","d":""}`)
	}
	server, err := h.store.CreateServer(r.Context(), req.EncryptedMetadata)
	if err != nil {
		slog.Error("createServer: create server", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create server"})
		return
	}
	if err := h.store.AddServerMember(r.Context(), server.ID, userID, models.PermissionLevelOwner); err != nil {
		slog.Error("createServer: add server member", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to add creator as guild owner"})
		return
	}
	// Server creation itself succeeded - respond immediately. Template channels are fire-and-forget.
	writeJSON(w, http.StatusCreated, server)

	// Resolve the channel template: explicit templateId > default template > hardcoded default.
	var template []models.TemplateChannel
	if req.TemplateID != nil {
		tmpl, err := h.store.GetServerTemplateByID(r.Context(), *req.TemplateID)
		if err == nil && tmpl != nil {
			template = tmpl.Channels
		}
	}
	if len(template) == 0 {
		tmpl, err := h.store.GetDefaultServerTemplate(r.Context())
		if err == nil && tmpl != nil {
			template = tmpl.Channels
		}
	}
	if len(template) == 0 {
		slog.Warn("createServer: no default server template found; using built-in fallback")
		template = fallbackTemplate()
	}
	// Ensure system channel is always present in the template.
	hasSystem := false
	for _, tc := range template {
		if tc.Type == "system" {
			hasSystem = true
			break
		}
	}
	if !hasSystem {
		// Name is display-only (metadata is encrypted); channel type determines behavior.
		template = append([]models.TemplateChannel{{Name: "system", Type: "system", Position: -1}}, template...)
	}

	ctx := r.Context()
	failures := 0

	// Pass 1: Create entries with no ParentRef (categories, top-level channels, system).
	// Build a map of template position -> created UUID for pass 2 parent resolution.
	// NOTE(0O-03): idempotency now uses GetChannelByTypeAndPosition (no name column).
	// Category map keyed by ParentRef name string kept for template compatibility.
	categoryPositionMap := map[int]string{} // position -> channel ID
	categoryNameMap := map[string]string{}  // tc.Name -> channel ID for ParentRef lookup
	for _, tc := range template {
		if tc.ParentRef != nil {
			continue // handled in pass 2
		}
		// Idempotency: skip if channel already exists at this position+type
		existing, err := h.store.GetChannelByTypeAndPosition(ctx, server.ID, tc.Type, tc.Position)
		if err != nil {
			slog.Error("createServer: idempotency check", "err", err, "position", tc.Position, "type", tc.Type)
			failures++
			continue
		}
		if existing != nil {
			if tc.Type == "category" {
				categoryPositionMap[tc.Position] = existing.ID
				categoryNameMap[tc.Name] = existing.ID
			}
			continue
		}
		// Seed encrypted_metadata with the hardcoded template name so channels
		// are visible before MLS is bootstrapped. This is safe because default
		// template names are server-defined constants, not user content.
		// NOTE: when user-created templates are implemented, user-chosen channel
		// names MUST be opaque - only hardcoded default templates get plaintext seeding.
		var meta []byte
		if tc.Name != "" {
			meta = []byte(`{"n":"` + tc.Name + `","d":""}`)
		}
		ch, err := h.store.CreateChannel(ctx, server.ID, meta, tc.Type, nil, tc.Position)
		if err != nil {
			slog.Error("createServer: create template channel", "err", err, "position", tc.Position)
			failures++
			continue
		}
		if tc.Type == "category" {
			categoryPositionMap[tc.Position] = ch.ID
			categoryNameMap[tc.Name] = ch.ID
		}
		// Broadcast channel_created.
		if h.hub != nil {
			msg, _ := json.Marshal(map[string]interface{}{
				"type":    "channel_created",
				"channel": ch,
			})
			h.hub.BroadcastToServer(server.ID, msg)
		}
	}

	// Pass 2: Create entries with ParentRef (channels under categories).
	for _, tc := range template {
		if tc.ParentRef == nil {
			continue
		}
		parentID, ok := categoryNameMap[*tc.ParentRef]
		if !ok {
			slog.Error("createServer: parent category not found", "parentRef", *tc.ParentRef)
			failures++
			continue
		}
		existing, err := h.store.GetChannelByTypeAndPosition(ctx, server.ID, tc.Type, tc.Position)
		if err != nil {
			slog.Error("createServer: idempotency check", "err", err, "position", tc.Position)
			failures++
			continue
		}
		if existing != nil {
			continue
		}
		// Same plaintext seeding as pass 1 - see note above re: user-created templates.
		var meta []byte
		if tc.Name != "" {
			meta = []byte(`{"n":"` + tc.Name + `","d":""}`)
		}
		ch, err := h.store.CreateChannel(ctx, server.ID, meta, tc.Type, &parentID, tc.Position)
		if err != nil {
			slog.Error("createServer: create template channel", "err", err, "position", tc.Position)
			failures++
			continue
		}
		if h.hub != nil {
			msg, _ := json.Marshal(map[string]interface{}{
				"type":    "channel_created",
				"channel": ch,
			})
			h.hub.BroadcastToServer(server.ID, msg)
		}
	}

	// Emit system message: server_created
	EmitSystemMessage(ctx, h.store, h.hub, server.ID, "server_created", userID, nil, "", nil)
	// If any template channels failed, emit partial failure system message.
	if failures > 0 {
		EmitSystemMessage(ctx, h.store, h.hub, server.ID, "template_partial_failure", userID, nil, "Some default channels could not be created", nil)
	}
}

// updateServer handles PUT /api/servers/{serverId}.
// Requires admin+ permission (level 2). Updates the encrypted_metadata blob for the guild.
// This is used in the two-step guild creation flow and after MLS epoch advances.
// On success, broadcasts a server_updated WS event to all guild members.
func (h *serversHandler) updateServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	level := guildLevelFromContext(r.Context())
	if level < models.PermissionLevelAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin level or higher required to update guild metadata"})
		return
	}
	var req struct {
		EncryptedMetadata []byte `json:"encryptedMetadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if err := h.store.UpdateServerEncryptedMetadata(r.Context(), serverID, req.EncryptedMetadata); err != nil {
		slog.Error("updateServer: update encrypted metadata", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update guild metadata"})
		return
	}
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":              "server_updated",
			"server_id":         serverID,
			"encryptedMetadata": req.EncryptedMetadata,
		})
		h.hub.BroadcastToServer(serverID, msg)
	}
	w.WriteHeader(http.StatusNoContent)
}

// listMyServers handles GET /api/servers - returns guilds the caller belongs to.
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
	level := guildLevelFromContext(r.Context())
	if level != models.PermissionLevelOwner {
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

// changePermissionLevelRequest is the JSON body for PUT /api/servers/{serverId}/members/{userId}/level.
type changePermissionLevelRequest struct {
	PermissionLevel int `json:"permissionLevel"`
}

// changeRole handles PUT /api/servers/{serverId}/members/{userId}/role.
// Temporarily wired to changePermissionLevel internally until Plan 03 rewrites this handler.
func (h *serversHandler) changeRole(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	targetUserID := chi.URLParam(r, "userId")
	actorID := userIDFromContext(r.Context())
	actorLevel := guildLevelFromContext(r.Context())

	if targetUserID == actorID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot change your own role"})
		return
	}
	if actorLevel < models.PermissionLevelAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin level or higher required"})
		return
	}
	var req changePermissionLevelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.PermissionLevel < models.PermissionLevelMember || req.PermissionLevel > models.PermissionLevelOwner {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "permissionLevel must be 0-3"})
		return
	}
	targetLevel, err := h.store.GetServerMemberLevel(r.Context(), serverID, targetUserID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "target user not found in this guild"})
		return
	}
	if actorLevel <= targetLevel {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot modify a member with equal or higher permission level"})
		return
	}
	if actorLevel <= req.PermissionLevel {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot assign a permission level equal or higher than your own"})
		return
	}
	if err := h.store.UpdateServerMemberLevel(r.Context(), serverID, targetUserID, req.PermissionLevel); err != nil {
		slog.Error("changeRole: update member level", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update permission level"})
		return
	}
	metadata := map[string]interface{}{
		"old_level": targetLevel,
		"new_level": req.PermissionLevel,
	}
	if err := h.store.InsertAuditLog(r.Context(), serverID, actorID, &targetUserID, "level_change", "permission level changed via guild management", metadata); err != nil {
		slog.Error("changeRole: insert audit log", "err", err)
	}
	EmitSystemMessage(r.Context(), h.store, h.hub, serverID, "role_changed", actorID, &targetUserID, "permission level changed", metadata)
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":             "member_role_changed",
			"server_id":        serverID,
			"user_id":          targetUserID,
			"permission_level": req.PermissionLevel,
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
	level := guildLevelFromContext(r.Context())
	if level == models.PermissionLevelOwner {
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

// joinServer handles POST /api/servers/{serverId}/join.
// Allows any authenticated, non-banned user to join an open and discoverable guild.
// Mounted OUTSIDE RequireGuildMember so non-members can reach it.
func (h *serversHandler) joinServer(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	server, err := h.store.GetServerByID(r.Context(), serverID)
	if err != nil {
		slog.Error("joinServer: get server", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to look up guild"})
		return
	}
	if server == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "guild not found"})
		return
	}

	// Check effective member cap: server override > instance default > unlimited.
	cfg, _ := h.store.GetInstanceConfig(r.Context())
	if cap := effectiveMemberCap(server, cfg); cap != nil {
		if server.MemberCount >= *cap {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "this server has reached its member limit"})
			return
		}
	}

	// Only open, discoverable guilds can be joined without an invite.
	// DM guilds are never joinable via this endpoint.
	if server.IsDm {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "DM guilds cannot be joined via this endpoint"})
		return
	}
	if !server.Discoverable {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "guild is not discoverable"})
		return
	}
	switch server.AccessPolicy {
	case "open":
		// Allowed - fall through.
	case "request":
		// MVP: return 202 Accepted with a descriptive message. Full request flow is future work.
		writeJSON(w, http.StatusAccepted, map[string]string{
			"message": "join request submitted - waiting for guild admin approval",
		})
		return
	default:
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "guild is not open to direct joins"})
		return
	}

	// Check whether the user is already a member.
	_, err = h.store.GetServerMemberLevel(r.Context(), serverID, userID)
	if err == nil {
		// No error means the row was found - user is already a member.
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already a member of this guild"})
		return
	}

	// Check for an active guild ban.
	ban, err := h.store.GetActiveBan(r.Context(), serverID, userID)
	if err != nil {
		slog.Error("joinServer: check ban", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify membership eligibility"})
		return
	}
	if ban != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "you are banned from this guild"})
		return
	}

	if err := h.store.AddServerMember(r.Context(), serverID, userID, models.PermissionLevelMember); err != nil {
		slog.Error("joinServer: add member", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to join guild"})
		return
	}
	if err := h.store.IncrementGuildMemberCount(r.Context(), serverID, 1); err != nil {
		slog.Error("joinServer: increment member count", "err", err)
		// Non-fatal: membership was added. Log and continue.
	}

	// Broadcast member_joined WS event (nil-check hub per CLAUDE.md pattern).
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":      "member_joined",
			"server_id": serverID,
			"user_id":   userID,
		})
		h.hub.BroadcastToServer(serverID, msg)
	}

	EmitSystemMessage(r.Context(), h.store, h.hub, serverID, "member_joined", userID, nil, "", nil)

	writeJSON(w, http.StatusCreated, server)
}

// listGuildBillingStats handles GET /api/admin/guilds.
// Instance owner only - returns exactly 5 fields per guild (privacy boundary).
func (h *serversHandler) listGuildBillingStats(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	instanceRole, err := h.store.GetUserRole(r.Context(), userID)
	if err != nil {
		slog.Error("listGuildBillingStats: get user role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
		return
	}
	if instanceRole != "admin" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "instance admin required"})
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
