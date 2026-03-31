package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/livekit"

	"github.com/go-chi/chi/v5"
)

const (
	maxRoomNameLen         = 256
	maxParticipantNameLen  = 128
	defaultParticipantName = "Participant"
	livekitTokenValidFor   = 12 * time.Hour
)

var roomNameRE = regexp.MustCompile(`^[a-zA-Z0-9._=-]+$`)

// LiveKitRoutes returns the route for POST /api/livekit/token (mount at /api/livekit).
func LiveKitRoutes(store db.Store, jwtSecret string, apiKey, apiSecret string) chi.Router {
	r := chi.NewRouter()
	h := &livekitHandler{
		store:     store,
		apiKey:    apiKey,
		apiSecret: apiSecret,
	}
	r.With(RequireAuth(jwtSecret, store)).Post("/token", h.token)
	return r
}

type livekitHandler struct {
	store     db.Store
	apiKey    string
	apiSecret string
}

func (h *livekitHandler) token(w http.ResponseWriter, r *http.Request) {
	if h.apiKey == "" || h.apiSecret == "" {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "LiveKit not configured"})
		return
	}
	var req struct {
		RoomName        string `json:"roomName"`
		ParticipantName string `json:"participantName"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	roomName := strings.TrimSpace(req.RoomName)
	if roomName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "roomName is required"})
		return
	}
	if len(roomName) > maxRoomNameLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "roomName too long"})
		return
	}
	if !roomNameRE.MatchString(roomName) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "roomName contains invalid characters"})
		return
	}
	participantName := strings.TrimSpace(req.ParticipantName)
	if participantName == "" {
		participantName = defaultParticipantName
	}
	if len(participantName) > maxParticipantNameLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "participantName too long"})
		return
	}
	for _, c := range participantName {
		if c < 32 && c != ' ' || c == 127 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "participantName must not contain control characters"})
			return
		}
	}
	userID := userIDFromContext(r.Context())

	// Mute check: deny token if the user is muted in the channel's server.
	// roomName format: "channel-{channelId}" — skip check for other formats.
	if h.store != nil {
		channelID := strings.TrimPrefix(roomName, "channel-")
		if channelID != roomName { // roomName started with "channel-"
			ch, err := h.store.GetChannelByID(r.Context(), channelID)
			if err == nil && ch != nil && ch.ServerID != nil {
				mute, err := h.store.GetActiveMute(r.Context(), *ch.ServerID, userID)
				if err == nil && mute != nil {
					writeJSON(w, http.StatusForbidden, map[string]string{
						"error": "You are muted in this server and cannot join voice channels.",
						"code":  "muted",
					})
					return
				}
			}
		}
	}

	tokenString, err := livekit.GenerateToken(h.apiKey, h.apiSecret, userID, roomName, participantName, livekitTokenValidFor)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": tokenString})
}
