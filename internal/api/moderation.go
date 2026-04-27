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

	"github.com/go-chi/chi/v5"
)

const (
	auditLogDefaultLimit = 50
	auditLogMaxLimit     = 100
)

// ModerationRoutes returns the router for guild-scoped moderation endpoints.
// Mounted under /api/servers/{serverId}/moderation.
// Auth and RequireGuildMember are applied by the parent router.
//
// roomService receives best-effort RemoveParticipant calls for any
// voice channel of the guild after a successful ban or kick. Pass
// livekit.NoopRoomService{} to disable eviction (e.g. dev / test).
func ModerationRoutes(store db.Store, hub GlobalBroadcaster, roomService livekit.RoomService) chi.Router {
	h := &moderationHandler{store: store, hub: hub, roomService: roomService}
	r := chi.NewRouter()
	r.Post("/kick", h.kickMember)
	r.Post("/ban", h.banMember)
	r.Post("/unban", h.unbanMember)
	r.Post("/mute", h.muteMember)
	r.Post("/unmute", h.unmuteMember)
	r.Delete("/messages/{messageId}", h.deleteMessage)
	r.Get("/audit-log", h.getAuditLog)
	r.Get("/bans", h.listBans)
	r.Get("/mutes", h.listMutes)
	return r
}

type moderationHandler struct {
	store       db.Store
	hub         GlobalBroadcaster
	roomService livekit.RoomService
}

// evictionRoomServiceTimeout caps the per-channel eviction call so a
// stalled LiveKit deployment cannot wedge a moderation request.
// The ban itself has already been committed before this runs, so a
// timeout here only delays the LiveKit-side cleanup.
const evictionRoomServiceTimeout = 4 * time.Second

// evictUserFromGuildVoice asks LiveKit to remove the target user
// from every voice channel of the guild. Failures are logged but
// never propagated: the database state is the source of truth, so
// the ban must succeed even if LiveKit is briefly unreachable.
// LiveKit eventual-consistency is the deliberate trade-off; see
// ans17.md for the failure-design rationale.
func (h *moderationHandler) evictUserFromGuildVoice(ctx context.Context, serverID, userID string) {
	if h.roomService == nil {
		return
	}
	channels, err := h.store.ListChannels(ctx, serverID)
	if err != nil {
		slog.Error("livekit eviction: list channels", "server_id", serverID, "err", err)
		return
	}
	for _, ch := range channels {
		if ch.Type != "voice" {
			continue
		}
		evictCtx, cancel := context.WithTimeout(ctx, evictionRoomServiceTimeout)
		room := "channel-" + ch.ID
		if err := h.roomService.RemoveParticipant(evictCtx, room, userID); err != nil {
			slog.Error("livekit eviction: remove participant",
				"room", room, "user_id", userID, "err", err)
		}
		cancel()
	}
}

// kickMember handles POST /api/servers/{serverId}/moderation/kick.
// Required guild role: mod+. Removes the target from the guild.
func (h *moderationHandler) kickMember(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	actorID := userIDFromContext(r.Context())
	if actorID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	var req models.KickRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.UserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "userId is required"})
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reason is required"})
		return
	}
	if req.UserID == actorID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot kick yourself"})
		return
	}
	actorLevel := guildLevelFromContext(r.Context())
	if actorLevel < models.PermissionLevelMod {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "mod level or higher required"})
		return
	}
	targetLevel, err := h.store.GetServerMemberLevel(r.Context(), serverID, req.UserID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "target user not found in this guild"})
		return
	}
	if actorLevel <= targetLevel {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot kick a member with equal or higher permission level"})
		return
	}
	if err := h.store.RemoveServerMember(r.Context(), serverID, req.UserID); err != nil {
		slog.Error("kick: remove server member", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to remove member"})
		return
	}
	h.evictUserFromGuildVoice(r.Context(), serverID, req.UserID)
	targetID := req.UserID
	if err := h.store.InsertAuditLog(r.Context(), serverID, actorID, &targetID, "kick", req.Reason, nil); err != nil {
		slog.Error("kick: insert audit log", "err", err)
	}
	EmitSystemMessage(r.Context(), h.store, h.hub, serverID, "member_kicked", actorID, &targetID, req.Reason, nil)
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":      "member_kicked",
			"server_id": serverID,
			"user_id":   req.UserID,
		})
		h.hub.BroadcastToServer(serverID, msg)
		kickedUID := req.UserID
		go func() {
			time.Sleep(500 * time.Millisecond)
			h.hub.DisconnectUser(kickedUID)
		}()
	}
	w.WriteHeader(http.StatusNoContent)
}

// banMember handles POST /api/servers/{serverId}/moderation/ban.
// Required guild role: admin+. Guild-scoped - does not affect other guilds (IROLE-04).
func (h *moderationHandler) banMember(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	actorID := userIDFromContext(r.Context())
	if actorID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	var req models.BanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.UserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "userId is required"})
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reason is required"})
		return
	}
	if req.UserID == actorID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot ban yourself"})
		return
	}
	actorLevel := guildLevelFromContext(r.Context())
	if actorLevel < models.PermissionLevelAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin level or higher required"})
		return
	}
	targetLevel, err := h.store.GetServerMemberLevel(r.Context(), serverID, req.UserID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "target user not found in this guild"})
		return
	}
	if actorLevel <= targetLevel {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot ban a member with equal or higher permission level"})
		return
	}
	var expiresAt *time.Time
	if req.ExpiresIn != nil {
		t := time.Now().Add(time.Duration(*req.ExpiresIn) * time.Second)
		expiresAt = &t
	}
	if _, err := h.store.InsertBan(r.Context(), serverID, req.UserID, actorID, req.Reason, expiresAt); err != nil {
		slog.Error("ban: insert ban", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create ban"})
		return
	}
	// Remove the banned user from the guild.
	if err := h.store.RemoveServerMember(r.Context(), serverID, req.UserID); err != nil {
		slog.Error("ban: remove server member", "err", err)
	}
	// Evict the banned user from any active LiveKit voice rooms
	// belonging to this guild. Best-effort; the ban itself has
	// already been committed regardless of the eviction outcome.
	h.evictUserFromGuildVoice(r.Context(), serverID, req.UserID)
	targetID := req.UserID
	metadata := map[string]interface{}{}
	if req.ExpiresIn != nil {
		metadata["expires_in"] = *req.ExpiresIn
	}
	if err := h.store.InsertAuditLog(r.Context(), serverID, actorID, &targetID, "ban", req.Reason, metadata); err != nil {
		slog.Error("ban: insert audit log", "err", err)
	}
	EmitSystemMessage(r.Context(), h.store, h.hub, serverID, "member_banned", actorID, &targetID, req.Reason, metadata)
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":      "member_banned",
			"server_id": serverID,
			"user_id":   req.UserID,
		})
		h.hub.BroadcastToServer(serverID, msg)
		// Delay disconnect so the writePump has time to deliver the broadcast
		// to the banned user before the connection is torn down.
		bannedUID := req.UserID
		go func() {
			time.Sleep(500 * time.Millisecond)
			h.hub.DisconnectUser(bannedUID)
		}()
	}
	w.WriteHeader(http.StatusNoContent)
}

// unbanMember handles POST /api/servers/{serverId}/moderation/unban.
// Required guild role: admin+. Lifts a guild-scoped active ban.
func (h *moderationHandler) unbanMember(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	actorID := userIDFromContext(r.Context())
	if actorID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	var req models.UnbanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.UserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "userId is required"})
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reason is required"})
		return
	}
	actorLevel := guildLevelFromContext(r.Context())
	if actorLevel < models.PermissionLevelAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin level or higher required"})
		return
	}
	ban, err := h.store.GetActiveBan(r.Context(), serverID, req.UserID)
	if err != nil {
		slog.Error("unban: get active ban", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to lookup ban"})
		return
	}
	if ban == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active ban found for this user"})
		return
	}
	if err := h.store.LiftBan(r.Context(), ban.ID, actorID); err != nil {
		slog.Error("unban: lift ban", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to lift ban"})
		return
	}
	targetID := req.UserID
	if err := h.store.InsertAuditLog(r.Context(), serverID, actorID, &targetID, "unban", req.Reason, nil); err != nil {
		slog.Error("unban: insert audit log", "err", err)
	}
	EmitSystemMessage(r.Context(), h.store, h.hub, serverID, "member_unbanned", actorID, &targetID, req.Reason, nil)
	w.WriteHeader(http.StatusNoContent)
}

// muteMember handles POST /api/servers/{serverId}/moderation/mute.
// Required guild role: mod+. Guild-scoped mute.
func (h *moderationHandler) muteMember(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	actorID := userIDFromContext(r.Context())
	if actorID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	var req models.MuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.UserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "userId is required"})
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reason is required"})
		return
	}
	if req.UserID == actorID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot mute yourself"})
		return
	}
	actorLevel := guildLevelFromContext(r.Context())
	if actorLevel < models.PermissionLevelMod {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "mod level or higher required"})
		return
	}
	targetLevel, err := h.store.GetServerMemberLevel(r.Context(), serverID, req.UserID)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "target user not found in this guild"})
		return
	}
	if actorLevel <= targetLevel {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot mute a member with equal or higher permission level"})
		return
	}
	var expiresAt *time.Time
	if req.ExpiresIn != nil {
		t := time.Now().Add(time.Duration(*req.ExpiresIn) * time.Second)
		expiresAt = &t
	}
	if _, err := h.store.InsertMute(r.Context(), serverID, req.UserID, actorID, req.Reason, expiresAt); err != nil {
		slog.Error("mute: insert mute", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create mute"})
		return
	}
	targetID := req.UserID
	metadata := map[string]interface{}{}
	if req.ExpiresIn != nil {
		metadata["expires_in"] = *req.ExpiresIn
	}
	if err := h.store.InsertAuditLog(r.Context(), serverID, actorID, &targetID, "mute", req.Reason, metadata); err != nil {
		slog.Error("mute: insert audit log", "err", err)
	}
	EmitSystemMessage(r.Context(), h.store, h.hub, serverID, "member_muted", actorID, &targetID, req.Reason, metadata)
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":      "member_muted",
			"server_id": serverID,
			"user_id":   req.UserID,
		})
		h.hub.BroadcastToServer(serverID, msg)
	}
	w.WriteHeader(http.StatusNoContent)
}

// unmuteMember handles POST /api/servers/{serverId}/moderation/unmute.
// Required guild role: mod+. Lifts a guild-scoped active mute.
func (h *moderationHandler) unmuteMember(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	actorID := userIDFromContext(r.Context())
	if actorID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	var req models.UnmuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if req.UserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "userId is required"})
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "reason is required"})
		return
	}
	actorLevel := guildLevelFromContext(r.Context())
	if actorLevel < models.PermissionLevelMod {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "mod level or higher required"})
		return
	}
	mute, err := h.store.GetActiveMute(r.Context(), serverID, req.UserID)
	if err != nil {
		slog.Error("unmute: get active mute", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to lookup mute"})
		return
	}
	if mute == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no active mute found for this user"})
		return
	}
	if err := h.store.LiftMute(r.Context(), mute.ID, actorID); err != nil {
		slog.Error("unmute: lift mute", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to lift mute"})
		return
	}
	targetID := req.UserID
	if err := h.store.InsertAuditLog(r.Context(), serverID, actorID, &targetID, "unmute", req.Reason, nil); err != nil {
		slog.Error("unmute: insert audit log", "err", err)
	}
	EmitSystemMessage(r.Context(), h.store, h.hub, serverID, "member_unmuted", actorID, &targetID, req.Reason, nil)
	w.WriteHeader(http.StatusNoContent)
}

// deleteMessage handles DELETE /api/servers/{serverId}/moderation/messages/{messageId}.
// Any user may delete their own message. Mod+ may delete any message.
func (h *moderationHandler) deleteMessage(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	actorID := userIDFromContext(r.Context())
	if actorID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	messageID := chi.URLParam(r, "messageId")
	if messageID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "messageId is required"})
		return
	}
	msg, err := h.store.GetMessageByID(r.Context(), messageID)
	if err != nil {
		slog.Error("deleteMessage: get message", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to lookup message"})
		return
	}
	if msg == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "message not found"})
		return
	}
	isSender := msg.SenderID != nil && *msg.SenderID == actorID
	if !isSender {
		actorLevel := guildLevelFromContext(r.Context())
		if actorLevel < models.PermissionLevelMod {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "mod level or higher required to delete others' messages"})
			return
		}
	}
	if err := h.store.DeleteMessage(r.Context(), messageID); err != nil {
		slog.Error("deleteMessage: delete message", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete message"})
		return
	}
	metadata := map[string]interface{}{
		"message_id": messageID,
		"sender_id":  msg.SenderID,
		"channel_id": msg.ChannelID,
	}
	if err := h.store.InsertAuditLog(r.Context(), serverID, actorID, msg.SenderID, "message_delete", "message deleted", metadata); err != nil {
		slog.Error("deleteMessage: insert audit log", "err", err)
	}
	if h.hub != nil {
		out, _ := json.Marshal(map[string]interface{}{
			"type":       "message_deleted",
			"message_id": messageID,
			"channel_id": msg.ChannelID,
		})
		h.hub.BroadcastToServer(serverID, out)
	}
	w.WriteHeader(http.StatusNoContent)
}

// listBans handles GET /api/servers/{serverId}/moderation/bans.
// Required guild role: admin+. Returns active (non-lifted, non-expired) bans for the guild.
func (h *moderationHandler) listBans(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	actorLevel := guildLevelFromContext(r.Context())
	if actorLevel < models.PermissionLevelAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin level or higher required"})
		return
	}
	bans, err := h.store.ListActiveBans(r.Context(), serverID)
	if err != nil {
		slog.Error("list bans", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list bans"})
		return
	}
	if bans == nil {
		bans = []models.Ban{}
	}
	writeJSON(w, http.StatusOK, bans)
}

// listMutes handles GET /api/servers/{serverId}/moderation/mutes.
// Required guild role: admin+. Returns active (non-lifted, non-expired) mutes for the guild.
func (h *moderationHandler) listMutes(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	actorLevel := guildLevelFromContext(r.Context())
	if actorLevel < models.PermissionLevelAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin level or higher required"})
		return
	}
	mutes, err := h.store.ListActiveMutes(r.Context(), serverID)
	if err != nil {
		slog.Error("list mutes", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list mutes"})
		return
	}
	if mutes == nil {
		mutes = []models.Mute{}
	}
	writeJSON(w, http.StatusOK, mutes)
}

// getAuditLog handles GET /api/servers/{serverId}/moderation/audit-log.
// Required guild role: admin+. Returns guild-scoped paginated audit log entries.
func (h *moderationHandler) getAuditLog(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	actorID := userIDFromContext(r.Context())
	if actorID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	actorLevel := guildLevelFromContext(r.Context())
	if actorLevel < models.PermissionLevelAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin level or higher required"})
		return
	}
	limit := auditLogDefaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > auditLogMaxLimit {
		limit = auditLogMaxLimit
	}
	offset := 0
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	var filter *db.AuditLogFilter
	actionParam := r.URL.Query().Get("action")
	actorParam := r.URL.Query().Get("actor_id")
	targetParam := r.URL.Query().Get("target_id")
	if actionParam != "" || actorParam != "" || targetParam != "" {
		filter = &db.AuditLogFilter{
			Action:   actionParam,
			ActorID:  actorParam,
			TargetID: targetParam,
		}
	}
	entries, err := h.store.ListAuditLog(r.Context(), serverID, limit, offset, filter)
	if err != nil {
		slog.Error("auditLog: list", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load audit log"})
		return
	}
	if entries == nil {
		entries = []models.AuditLogEntry{}
	}
	writeJSON(w, http.StatusOK, entries)
}
