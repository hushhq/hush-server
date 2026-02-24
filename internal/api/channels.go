package api

import (
	"encoding/base64"
	"net/http"
	"strconv"
	"time"

	"hush.app/server/internal/db"

	"github.com/go-chi/chi/v5"
)

const (
	channelMessagesLimitDefault = 50
	channelMessagesLimitMax     = 50
)

// ChannelRoutes returns the router for /api/channels. Phase C: only GET :id/messages.
func ChannelRoutes(store db.Store, jwtSecret string) chi.Router {
	r := chi.NewRouter()
	h := &channelsHandler{store: store}
	r.Use(RequireAuth(jwtSecret, store))
	r.Get("/{id}/messages", h.getMessages)
	return r
}

type channelsHandler struct {
	store db.Store
}

// messageResponse is the JSON shape for one message (ciphertext as base64 string).
type messageResponse struct {
	ID         string `json:"id"`
	ChannelID  string `json:"channelId"`
	SenderID   string `json:"senderId"`
	Ciphertext string `json:"ciphertext"` // base64
	Timestamp  string `json:"timestamp"`  // RFC3339Nano
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
	messages, err := h.store.GetMessages(ctx, channelID, userID, before, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to load messages"})
		return
	}
	out := make([]messageResponse, 0, len(messages))
	for _, m := range messages {
		out = append(out, messageResponse{
			ID:         m.ID,
			ChannelID:  m.ChannelID,
			SenderID:   m.SenderID,
			Ciphertext: base64.StdEncoding.EncodeToString(m.Ciphertext),
			Timestamp:  m.Timestamp.Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, out)
}
