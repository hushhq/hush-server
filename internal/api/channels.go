package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/models"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

const (
	channelMessagesLimitDefault = 50
	channelMessagesLimitMax     = 50
	maxNameLength               = 100
)

// ChannelRoutes returns the router for channels nested under /api/servers/{serverId}.
// Auth and RequireGuildMember are applied by the parent router; this router
// only adds channel-specific routes.
func ChannelRoutes(store db.Store, hub GlobalBroadcaster) chi.Router {
	r := chi.NewRouter()
	h := &channelsHandler{store: store, hub: hub}
	r.Post("/", h.createChannel)
	r.Get("/", h.listChannels)
	r.Get("/{id}/messages", h.getMessages)
	r.Delete("/{id}", h.deleteChannel)
	r.Put("/{id}/move", h.moveChannel)
	return r
}

type channelsHandler struct {
	store db.Store
	hub   GlobalBroadcaster
}

// messageResponse is the JSON shape for one message (ciphertext as base64 string).
type messageResponse struct {
	ID                string  `json:"id"`
	ChannelID         string  `json:"channelId"`
	SenderID          *string `json:"senderId,omitempty"`
	FederatedSenderID *string `json:"federatedSenderId,omitempty"`
	Ciphertext        string  `json:"ciphertext"` // base64
	Timestamp         string  `json:"timestamp"`  // RFC3339Nano
}

func (h *channelsHandler) createChannel(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	level := guildLevelFromContext(r.Context())
	if level < models.PermissionLevelAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin level or higher required to create channels"})
		return
	}
	var req models.CreateChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	// Plaintext name fallback: when MLS is not bootstrapped, clients send Name
	// instead of EncryptedMetadata. Wrap it as a JSON blob so the client can
	// read it back without a decryption key.
	if len(req.EncryptedMetadata) == 0 && req.Name != "" {
		req.EncryptedMetadata = []byte(`{"n":"` + req.Name + `","d":""}`)
	}
	if req.Type != "text" && req.Type != "voice" && req.Type != "category" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "type must be text, voice, or category"})
		return
	}
	position := 0
	if req.Position != nil {
		position = *req.Position
	}
	if req.Type == "category" {
		if req.ParentID != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "categories cannot be nested"})
			return
		}
	}
	ch, err := h.store.CreateChannel(r.Context(), serverID, req.EncryptedMetadata, req.Type, req.ParentID, position)
	if err != nil {
		slog.Error("create channel", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to create channel"})
		return
	}
	writeJSON(w, http.StatusCreated, ch)
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":    "channel_created",
			"channel": ch,
		})
		h.hub.BroadcastToServer(serverID, msg)
	}
}

func (h *channelsHandler) listChannels(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	channels, err := h.store.ListChannels(r.Context(), serverID)
	if err != nil {
		slog.Error("list channels", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list channels"})
		return
	}
	if channels == nil {
		channels = []models.Channel{}
	}
	writeJSON(w, http.StatusOK, channels)
}

func (h *channelsHandler) getMessages(w http.ResponseWriter, r *http.Request) {
	channelID := chi.URLParam(r, "id")
	if channelID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "channel id required"})
		return
	}
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	beforeStr := r.URL.Query().Get("before")
	afterStr := r.URL.Query().Get("after")
	limitStr := r.URL.Query().Get("limit")
	limit := channelMessagesLimitDefault
	if limitStr != "" {
		n, err := strconv.Atoi(limitStr)
		if err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid limit"})
			return
		}
		if n > channelMessagesLimitMax {
			n = channelMessagesLimitMax
		}
		limit = n
	}
	if beforeStr != "" && afterStr != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "before and after are mutually exclusive"})
		return
	}
	var before time.Time
	if beforeStr != "" {
		var err error
		before, err = time.Parse(time.RFC3339Nano, beforeStr)
		if err != nil {
			before, err = time.Parse(time.RFC3339, beforeStr)
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid before timestamp"})
			return
		}
	}
	var after time.Time
	if afterStr != "" {
		var err error
		after, err = time.Parse(time.RFC3339Nano, afterStr)
		if err != nil {
			after, err = time.Parse(time.RFC3339, afterStr)
		}
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid after timestamp"})
			return
		}
	}
	ctx := r.Context()
	ok, err := h.store.IsChannelMember(ctx, channelID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to check membership"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a channel member"})
		return
	}
	var messages []models.Message
	if !after.IsZero() {
		messages, err = h.store.GetMessagesAfter(ctx, channelID, userID, after, limit)
	} else {
		messages, err = h.store.GetMessages(ctx, channelID, userID, before, limit)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load messages"})
		return
	}
	out := make([]messageResponse, 0, len(messages))
	for _, m := range messages {
		out = append(out, messageResponse{
			ID:                m.ID,
			ChannelID:         m.ChannelID,
			SenderID:          m.SenderID,
			FederatedSenderID: m.FederatedSenderID,
			Ciphertext:        base64.StdEncoding.EncodeToString(m.Ciphertext),
			Timestamp:         m.Timestamp.Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *channelsHandler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	channelID := chi.URLParam(r, "id")
	if channelID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "channel id required"})
		return
	}
	level := guildLevelFromContext(r.Context())
	if level < models.PermissionLevelAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin level or higher required to delete channel"})
		return
	}
	// Resolve the channel and confirm it belongs to this guild before
	// any further checks. A foreign-guild channel is reported as 404 to
	// avoid confirming its existence.
	ch, err := h.store.GetChannelByID(r.Context(), channelID)
	if err != nil {
		slog.Error("delete channel: get channel", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get channel"})
		return
	}
	if ch == nil || ch.ServerID == nil || *ch.ServerID != serverID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return
	}
	// Block deletion of system channels.
	if ch.Type == "system" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "system channels cannot be deleted"})
		return
	}
	if err := h.store.DeleteChannel(r.Context(), channelID, serverID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
			return
		}
		slog.Error("delete channel", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete channel"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":       "channel_deleted",
			"channel_id": channelID,
		})
		h.hub.BroadcastToServer(serverID, msg)
	}
}

func (h *channelsHandler) moveChannel(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")
	channelID := chi.URLParam(r, "id")
	if channelID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "channel id required"})
		return
	}
	level := guildLevelFromContext(r.Context())
	if level < models.PermissionLevelAdmin {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin level or higher required"})
		return
	}
	var req models.MoveChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Position < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "position must be >= 0"})
		return
	}
	// Resolve the channel and confirm it belongs to this guild before
	// any further checks. A foreign-guild channel is reported as 404.
	ch, err := h.store.GetChannelByID(r.Context(), channelID)
	if err != nil {
		slog.Error("move channel: get channel", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get channel"})
		return
	}
	if ch == nil || ch.ServerID == nil || *ch.ServerID != serverID {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return
	}
	// Block moves on system channels.
	if ch.Type == "system" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "system channels cannot be moved"})
		return
	}
	if req.ParentID != nil {
		parent, err := h.store.GetChannelByID(r.Context(), *req.ParentID)
		if err != nil || parent == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "parent channel not found"})
			return
		}
		// A parent in another guild is treated as 404 — the parent does
		// not exist from this guild's perspective.
		if parent.ServerID == nil || *parent.ServerID != serverID {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "parent channel not found"})
			return
		}
		if parent.Type != "category" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "parent must be a category channel"})
			return
		}
	}
	if err := h.store.MoveChannel(r.Context(), channelID, serverID, req.ParentID, req.Position); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
			return
		}
		slog.Error("move channel", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to move channel"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
	if h.hub != nil {
		msg, _ := json.Marshal(map[string]interface{}{
			"type":       "channel_moved",
			"channel_id": channelID,
			"parent_id":  req.ParentID,
			"position":   req.Position,
		})
		h.hub.BroadcastToServer(serverID, msg)
	}
}

