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
	return LiveKitRoutesWithVoiceStateAndPublicURL(store, jwtSecret, apiKey, apiSecret, "", nil)
}

// LiveKitRoutesWithVoiceState returns the LiveKit API routes backed by the
// supplied in-memory voice tracker. The tracker is optional for tests that only
// exercise token issuance.
func LiveKitRoutesWithVoiceState(store db.Store, jwtSecret string, apiKey, apiSecret string, state *VoiceState) chi.Router {
	return LiveKitRoutesWithVoiceStateAndPublicURL(store, jwtSecret, apiKey, apiSecret, "", state)
}

// LiveKitRoutesWithVoiceStateAndPublicURL returns the LiveKit API routes backed
// by the supplied in-memory voice tracker and optional public signaling URL.
func LiveKitRoutesWithVoiceStateAndPublicURL(store db.Store, jwtSecret string, apiKey, apiSecret, publicURL string, state *VoiceState) chi.Router {
	r := chi.NewRouter()
	h := &livekitHandler{
		store:      store,
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		publicURL:  strings.TrimSpace(publicURL),
		voiceState: state,
	}
	r.With(RequireAuth(jwtSecret, store)).Post("/token", h.token)
	r.With(RequireAuth(jwtSecret, store)).Get("/voice-state", h.voiceStateSnapshot)
	return r
}

type livekitHandler struct {
	store      db.Store
	apiKey     string
	apiSecret  string
	publicURL  string
	voiceState *VoiceState
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

	// Moderation gate: deny token issuance when an active ban or mute
	// applies. Order matters: the instance ban is the broadest signal
	// (covers any room), then the per-guild ban (covers any voice
	// channel of that guild), then mute (specific to the target
	// guild's voice channels). The first matching reason short-circuits
	// the rest so the response carries a single, accurate code.
	if h.store != nil {
		instanceBan, err := h.store.GetActiveInstanceBan(r.Context(), userID)
		if err == nil && instanceBan != nil {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "You are banned from this instance and cannot join voice channels.",
				"code":  "instance_banned",
			})
			return
		}

		// Per-guild checks require a "channel-{channelId}" room. Other
		// room name formats (legacy, ad-hoc) bypass these because they
		// have no resolvable guild scope.
		channelID := strings.TrimPrefix(roomName, "channel-")
		if channelID != roomName {
			ch, err := h.store.GetChannelByID(r.Context(), channelID)
			if err == nil && ch != nil && ch.ServerID != nil {
				if ban, err := h.store.GetActiveBan(r.Context(), *ch.ServerID, userID); err == nil && ban != nil {
					writeJSON(w, http.StatusForbidden, map[string]string{
						"error": "You are banned from this server and cannot join voice channels.",
						"code":  "banned",
					})
					return
				}
				if _, err := h.store.GetServerMemberLevel(r.Context(), *ch.ServerID, userID); err != nil {
					writeJSON(w, http.StatusForbidden, map[string]string{
						"error": "You are not a member of this server.",
						"code":  "not_member",
					})
					return
				}
				if mute, err := h.store.GetActiveMute(r.Context(), *ch.ServerID, userID); err == nil && mute != nil {
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
	body := map[string]string{"token": tokenString}
	if h.publicURL != "" {
		body["livekitUrl"] = h.publicURL
	}
	writeJSON(w, http.StatusOK, body)
}

func (h *livekitHandler) voiceStateSnapshot(w http.ResponseWriter, r *http.Request) {
	serverID := strings.TrimSpace(r.URL.Query().Get("serverId"))
	if serverID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "serverId is required"})
		return
	}
	if h.store == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "store unavailable"})
		return
	}
	userID := userIDFromContext(r.Context())
	if _, err := h.store.GetServerMemberLevel(r.Context(), serverID, userID); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a server member"})
		return
	}
	channels, err := h.store.ListChannels(r.Context(), serverID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list channels"})
		return
	}
	voiceChannelIDs := make(map[string]struct{})
	for _, channel := range channels {
		if channel.Type == "voice" {
			voiceChannelIDs[channel.ID] = struct{}{}
		}
	}
	snapshot := map[string][]voiceParticipant{}
	if h.voiceState != nil && len(voiceChannelIDs) > 0 {
		snapshot = h.voiceState.snapshotByChannel(voiceChannelIDs)
	}
	writeJSON(w, http.StatusOK, map[string]map[string][]voiceParticipant{
		"participantsByChannel": snapshot,
	})
}
