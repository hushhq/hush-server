package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/webhook"

	"hush.app/server/internal/ws"
)

// voiceParticipant is a single participant in a voice channel.
type voiceParticipant struct {
	UserID      string `json:"userId"`
	DisplayName string `json:"displayName"`
}

// voiceState tracks participants per LiveKit room (in-memory, rebuilt from webhooks).
type voiceState struct {
	mu    sync.RWMutex
	rooms map[string]map[string]voiceParticipant // roomName -> identity -> participant
}

func newVoiceState() *voiceState {
	return &voiceState{rooms: make(map[string]map[string]voiceParticipant)}
}

func (vs *voiceState) join(roomName, identity, displayName string) []voiceParticipant {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if vs.rooms[roomName] == nil {
		vs.rooms[roomName] = make(map[string]voiceParticipant)
	}
	vs.rooms[roomName][identity] = voiceParticipant{UserID: identity, DisplayName: displayName}
	return vs.listLocked(roomName)
}

func (vs *voiceState) leave(roomName, identity string) []voiceParticipant {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if m := vs.rooms[roomName]; m != nil {
		delete(m, identity)
		if len(m) == 0 {
			delete(vs.rooms, roomName)
		}
	}
	return vs.listLocked(roomName)
}

func (vs *voiceState) listLocked(roomName string) []voiceParticipant {
	m := vs.rooms[roomName]
	result := make([]voiceParticipant, 0, len(m))
	for _, p := range m {
		result = append(result, p)
	}
	return result
}

// parseRoomName extracts channelID from "channel-{cid}" (single-tenant naming).
// Also handles legacy "server-{sid}-channel-{cid}" format during transition.
func parseRoomName(roomName string) (channelID string, ok bool) {
	const flatPrefix = "channel-"
	const legacySep = "-channel-"
	if strings.HasPrefix(roomName, flatPrefix) {
		channelID = roomName[len(flatPrefix):]
		return channelID, channelID != ""
	}
	idx := strings.Index(roomName, legacySep)
	if idx < 0 {
		return "", false
	}
	channelID = roomName[idx+len(legacySep):]
	return channelID, channelID != ""
}

// LiveKitWebhookHandler returns an HTTP handler for LiveKit webhook events.
// It tracks voice participants in-memory and broadcasts voice_state_update
// to all WS clients subscribed to the affected server.
func LiveKitWebhookHandler(hub *ws.Hub, apiKey, apiSecret string) http.HandlerFunc {
	provider := auth.NewSimpleKeyProvider(apiKey, apiSecret)
	state := newVoiceState()

	return func(w http.ResponseWriter, r *http.Request) {
		event, err := webhook.ReceiveWebhookEvent(r, provider)
		if err != nil {
			slog.Warn("livekit webhook validation failed", "err", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		room := event.GetRoom()
		participant := event.GetParticipant()
		if room == nil || participant == nil {
			w.WriteHeader(http.StatusOK)
			return
		}

		channelID, ok := parseRoomName(room.GetName())
		if !ok {
			slog.Debug("livekit webhook: ignoring non-hush room", "room", room.GetName())
			w.WriteHeader(http.StatusOK)
			return
		}

		var participants []voiceParticipant
		switch event.GetEvent() {
		case webhook.EventParticipantJoined:
			participants = state.join(room.GetName(), participant.GetIdentity(), participantDisplayName(participant))
			slog.Info("voice join", "channel", channelID, "user", participant.GetIdentity())
		case webhook.EventParticipantLeft:
			participants = state.leave(room.GetName(), participant.GetIdentity())
			slog.Info("voice leave", "channel", channelID, "user", participant.GetIdentity())
		default:
			w.WriteHeader(http.StatusOK)
			return
		}

		msg, _ := json.Marshal(map[string]interface{}{
			"type":         "voice_state_update",
			"channel_id":   channelID,
			"participants": participants,
		})
		hub.BroadcastToAll(msg)

		w.WriteHeader(http.StatusOK)
	}
}

func participantDisplayName(p *livekit.ParticipantInfo) string {
	if p.GetName() != "" {
		return p.GetName()
	}
	return p.GetIdentity()
}
