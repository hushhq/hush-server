package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/models"

	"github.com/go-chi/chi/v5"
)

const (
	systemMessagesLimitDefault = 50
	systemMessagesLimitMax     = 50
)

// SystemMessagesRoutes returns a chi.Router for system message endpoints.
// Mounted under /api/servers/{serverId}/system-messages.
func SystemMessagesRoutes(store db.Store) chi.Router {
	r := chi.NewRouter()
	h := &systemMessagesHandler{store: store}
	r.Get("/", h.listSystemMessages)
	return r
}

type systemMessagesHandler struct {
	store db.Store
}

// listSystemMessages handles GET /api/servers/{serverId}/system-messages.
// Supports ?before=RFC3339 and ?limit=N (default 50, max 50).
func (h *systemMessagesHandler) listSystemMessages(w http.ResponseWriter, r *http.Request) {
	serverID := chi.URLParam(r, "serverId")

	beforeStr := r.URL.Query().Get("before")
	limitStr := r.URL.Query().Get("limit")

	limit := systemMessagesLimitDefault
	if limitStr != "" {
		n, err := strconv.Atoi(limitStr)
		if err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid limit"})
			return
		}
		if n > systemMessagesLimitMax {
			n = systemMessagesLimitMax
		}
		limit = n
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

	messages, err := h.store.ListSystemMessages(r.Context(), serverID, before, limit)
	if err != nil {
		slog.Error("list system messages", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list system messages"})
		return
	}
	if messages == nil {
		messages = []models.SystemMessage{}
	}
	writeJSON(w, http.StatusOK, messages)
}

// EmitSystemMessage inserts a system message and broadcasts it via WebSocket.
// Fire-and-forget: logs errors but does not return them (same pattern as audit log).
func EmitSystemMessage(ctx context.Context, store db.Store, hub GlobalBroadcaster, serverID string, eventType string, actorID string, targetID *string, reason string, metadata map[string]interface{}) {
	msg, err := store.InsertSystemMessage(ctx, serverID, eventType, actorID, targetID, reason, metadata)
	if err != nil {
		slog.Error("emit system message: insert failed", "err", err, "eventType", eventType, "serverID", serverID)
		return
	}
	if hub == nil {
		return
	}
	payload, err := json.Marshal(map[string]interface{}{
		"type":           "system_message",
		"server_id":      serverID,
		"system_message": msg,
	})
	if err != nil {
		slog.Error("emit system message: marshal failed", "err", err)
		return
	}
	hub.BroadcastToServer(serverID, payload)
}
