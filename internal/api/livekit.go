package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"hush.app/server/internal/db"
	"hush.app/server/internal/livekit"

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
		apiKey:    apiKey,
		apiSecret: apiSecret,
	}
	r.With(RequireAuth(jwtSecret, store)).Post("/token", h.token)
	return r
}

type livekitHandler struct {
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
	tokenString, err := livekit.GenerateToken(h.apiKey, h.apiSecret, userID, roomName, participantName, livekitTokenValidFor)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token generation failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": tokenString})
}
