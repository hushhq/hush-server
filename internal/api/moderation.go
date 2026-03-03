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

const (
	auditLogDefaultLimit = 50
	auditLogMaxLimit     = 100
)

// ModerationRoutes returns the router for /api/moderation.
// All endpoints require authentication and role verification.
func ModerationRoutes(store db.Store, hub GlobalBroadcaster, jwtSecret string) chi.Router {
	h := &moderationHandler{store: store, hub: hub}
	r := chi.NewRouter()
	r.Use(RequireAuth(jwtSecret, store))
	r.Post("/kick", h.kickMember)
	r.Post("/ban", h.banMember)
	r.Post("/unban", h.unbanMember)
	r.Post("/mute", h.muteMember)
	r.Post("/unmute", h.unmuteMember)
	r.Put("/role", h.changeRole)
	r.Delete("/messages/{messageId}", h.deleteMessage)
	r.Get("/audit-log", h.getAuditLog)
	return r
}

type moderationHandler struct {
	store db.Store
	hub   GlobalBroadcaster
}

// kickMember handles POST /api/moderation/kick.
// Required role: mod+. Removes the target's sessions, disconnects via WS, and broadcasts member_kicked.
func (h *moderationHandler) kickMember(w http.ResponseWriter, r *http.Request) {
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
	actorRole, err := h.store.GetUserRole(r.Context(), actorID)
	if err != nil {
		slog.Error("kick: get actor role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
		return
	}
	if !roleAtLeast(actorRole, "mod") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "mod role or higher required"})
		return
	}
	targetRole, err := h.store.GetUserRole(r.Context(), req.UserID)
	if err != nil {
		slog.Error("kick: get target role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify target role"})
		return
	}
	if roleOrder[actorRole] <= roleOrder[targetRole] {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot kick a member with equal or higher role"})
		return
	}
	if err := h.store.DeleteSessionsByUserID(r.Context(), req.UserID); err != nil {
		slog.Error("kick: delete sessions", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to invalidate sessions"})
		return
	}
	if h.hub != nil {
		h.hub.DisconnectUser(req.UserID)
	}
	targetID := req.UserID
	if err := h.store.InsertAuditLog(r.Context(), actorID, &targetID, "kick", req.Reason, nil); err != nil {
		slog.Error("kick: insert audit log", "err", err)
	}
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":    "member_kicked",
			"user_id": req.UserID,
		})
		h.hub.BroadcastToAll(msg)
	}
	w.WriteHeader(http.StatusNoContent)
}

// banMember handles POST /api/moderation/ban.
// Required role: admin+. Creates a ban record, invalidates sessions, and disconnects the user.
func (h *moderationHandler) banMember(w http.ResponseWriter, r *http.Request) {
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
	actorRole, err := h.store.GetUserRole(r.Context(), actorID)
	if err != nil {
		slog.Error("ban: get actor role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
		return
	}
	if !roleAtLeast(actorRole, "admin") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role or higher required"})
		return
	}
	targetRole, err := h.store.GetUserRole(r.Context(), req.UserID)
	if err != nil {
		slog.Error("ban: get target role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify target role"})
		return
	}
	if roleOrder[actorRole] <= roleOrder[targetRole] {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot ban a member with equal or higher role"})
		return
	}
	var expiresAt *time.Time
	if req.ExpiresIn != nil {
		t := time.Now().Add(time.Duration(*req.ExpiresIn) * time.Second)
		expiresAt = &t
	}
	if _, err := h.store.InsertBan(r.Context(), req.UserID, actorID, req.Reason, expiresAt); err != nil {
		slog.Error("ban: insert ban", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create ban"})
		return
	}
	if err := h.store.DeleteSessionsByUserID(r.Context(), req.UserID); err != nil {
		slog.Error("ban: delete sessions", "err", err)
	}
	if h.hub != nil {
		h.hub.DisconnectUser(req.UserID)
	}
	targetID := req.UserID
	metadata := map[string]interface{}{}
	if req.ExpiresIn != nil {
		metadata["expires_in"] = *req.ExpiresIn
	}
	if err := h.store.InsertAuditLog(r.Context(), actorID, &targetID, "ban", req.Reason, metadata); err != nil {
		slog.Error("ban: insert audit log", "err", err)
	}
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":    "member_banned",
			"user_id": req.UserID,
		})
		h.hub.BroadcastToAll(msg)
	}
	w.WriteHeader(http.StatusNoContent)
}

// unbanMember handles POST /api/moderation/unban.
// Required role: admin+. Lifts an active ban.
func (h *moderationHandler) unbanMember(w http.ResponseWriter, r *http.Request) {
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
	actorRole, err := h.store.GetUserRole(r.Context(), actorID)
	if err != nil {
		slog.Error("unban: get actor role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
		return
	}
	if !roleAtLeast(actorRole, "admin") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role or higher required"})
		return
	}
	ban, err := h.store.GetActiveBan(r.Context(), req.UserID)
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
	if err := h.store.InsertAuditLog(r.Context(), actorID, &targetID, "unban", req.Reason, nil); err != nil {
		slog.Error("unban: insert audit log", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// muteMember handles POST /api/moderation/mute.
// Required role: mod+. Creates a mute record so the user cannot send messages.
func (h *moderationHandler) muteMember(w http.ResponseWriter, r *http.Request) {
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
	actorRole, err := h.store.GetUserRole(r.Context(), actorID)
	if err != nil {
		slog.Error("mute: get actor role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
		return
	}
	if !roleAtLeast(actorRole, "mod") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "mod role or higher required"})
		return
	}
	targetRole, err := h.store.GetUserRole(r.Context(), req.UserID)
	if err != nil {
		slog.Error("mute: get target role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify target role"})
		return
	}
	if roleOrder[actorRole] <= roleOrder[targetRole] {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot mute a member with equal or higher role"})
		return
	}
	var expiresAt *time.Time
	if req.ExpiresIn != nil {
		t := time.Now().Add(time.Duration(*req.ExpiresIn) * time.Second)
		expiresAt = &t
	}
	if _, err := h.store.InsertMute(r.Context(), req.UserID, actorID, req.Reason, expiresAt); err != nil {
		slog.Error("mute: insert mute", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create mute"})
		return
	}
	targetID := req.UserID
	metadata := map[string]interface{}{}
	if req.ExpiresIn != nil {
		metadata["expires_in"] = *req.ExpiresIn
	}
	if err := h.store.InsertAuditLog(r.Context(), actorID, &targetID, "mute", req.Reason, metadata); err != nil {
		slog.Error("mute: insert audit log", "err", err)
	}
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":    "member_muted",
			"user_id": req.UserID,
		})
		h.hub.BroadcastToAll(msg)
	}
	w.WriteHeader(http.StatusNoContent)
}

// unmuteMember handles POST /api/moderation/unmute.
// Required role: mod+. Lifts an active mute.
func (h *moderationHandler) unmuteMember(w http.ResponseWriter, r *http.Request) {
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
	actorRole, err := h.store.GetUserRole(r.Context(), actorID)
	if err != nil {
		slog.Error("unmute: get actor role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
		return
	}
	if !roleAtLeast(actorRole, "mod") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "mod role or higher required"})
		return
	}
	mute, err := h.store.GetActiveMute(r.Context(), req.UserID)
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
	if err := h.store.InsertAuditLog(r.Context(), actorID, &targetID, "unmute", req.Reason, nil); err != nil {
		slog.Error("unmute: insert audit log", "err", err)
	}
	w.WriteHeader(http.StatusNoContent)
}

// changeRole handles PUT /api/moderation/role.
// Required role: admin+. Changes a member's role with strict hierarchy enforcement.
func (h *moderationHandler) changeRole(w http.ResponseWriter, r *http.Request) {
	actorID := userIDFromContext(r.Context())
	if actorID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	var req models.ChangeRoleRequest
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
	switch req.NewRole {
	case "member", "mod", "admin":
		// valid
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "newRole must be member, mod, or admin"})
		return
	}
	if req.UserID == actorID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot change your own role"})
		return
	}
	actorRole, err := h.store.GetUserRole(r.Context(), actorID)
	if err != nil {
		slog.Error("changeRole: get actor role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
		return
	}
	if !roleAtLeast(actorRole, "admin") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role or higher required"})
		return
	}
	targetRole, err := h.store.GetUserRole(r.Context(), req.UserID)
	if err != nil {
		slog.Error("changeRole: get target role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify target role"})
		return
	}
	// Actor must outrank both the current role and the new role being assigned.
	if roleOrder[actorRole] <= roleOrder[targetRole] {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot modify a member with equal or higher role"})
		return
	}
	if roleOrder[actorRole] <= roleOrder[req.NewRole] {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "cannot assign a role equal or higher than your own"})
		return
	}
	if err := h.store.UpdateUserRole(r.Context(), req.UserID, req.NewRole); err != nil {
		slog.Error("changeRole: update user role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update role"})
		return
	}
	targetID := req.UserID
	metadata := map[string]interface{}{
		"old_role": targetRole,
		"new_role": req.NewRole,
	}
	if err := h.store.InsertAuditLog(r.Context(), actorID, &targetID, "role_change", req.Reason, metadata); err != nil {
		slog.Error("changeRole: insert audit log", "err", err)
	}
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":     "role_changed",
			"user_id":  req.UserID,
			"new_role": req.NewRole,
		})
		h.hub.BroadcastToAll(msg)
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteMessage handles DELETE /api/moderation/messages/{messageId}.
// Any user may delete their own message. Mod+ may delete any message.
func (h *moderationHandler) deleteMessage(w http.ResponseWriter, r *http.Request) {
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
	// Determine if the actor is the sender or has mod+ role.
	isSender := msg.SenderID == actorID
	if !isSender {
		actorRole, err := h.store.GetUserRole(r.Context(), actorID)
		if err != nil {
			slog.Error("deleteMessage: get actor role", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
			return
		}
		if !roleAtLeast(actorRole, "mod") {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "mod role or higher required to delete others' messages"})
			return
		}
	}
	if err := h.store.DeleteMessage(r.Context(), messageID); err != nil {
		slog.Error("deleteMessage: delete message", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete message"})
		return
	}
	senderID := msg.SenderID
	metadata := map[string]interface{}{
		"message_id": messageID,
		"sender_id":  senderID,
		"channel_id": msg.ChannelID,
	}
	if err := h.store.InsertAuditLog(r.Context(), actorID, &senderID, "message_delete", "message deleted", metadata); err != nil {
		slog.Error("deleteMessage: insert audit log", "err", err)
	}
	if h.hub != nil {
		out, _ := json.Marshal(map[string]interface{}{
			"type":       "message_deleted",
			"message_id": messageID,
			"channel_id": msg.ChannelID,
		})
		h.hub.BroadcastToAll(out)
	}
	w.WriteHeader(http.StatusNoContent)
}

// getAuditLog handles GET /api/moderation/audit-log.
// Required role: admin+. Returns paginated audit log entries.
func (h *moderationHandler) getAuditLog(w http.ResponseWriter, r *http.Request) {
	actorID := userIDFromContext(r.Context())
	if actorID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	actorRole, err := h.store.GetUserRole(r.Context(), actorID)
	if err != nil {
		slog.Error("auditLog: get actor role", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to verify role"})
		return
	}
	if !roleAtLeast(actorRole, "admin") {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin role or higher required"})
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
	entries, err := h.store.ListAuditLog(r.Context(), limit, offset)
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
