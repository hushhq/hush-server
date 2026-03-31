package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/webhook"

	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/ws"
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

const webhookChannelLookupTimeout = 5 * time.Second

// LiveKitWebhookHandler returns an HTTP handler for LiveKit webhook events.
// It tracks voice participants in-memory and broadcasts voice_state_update
// to the guild's WS subscribers via BroadcastToServer.
// Falls back to BroadcastToAll if the channel's server ID cannot be resolved.
func LiveKitWebhookHandler(hub *ws.Hub, store db.Store, apiKey, apiSecret string) http.HandlerFunc {
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

		// Resolve the channel's server_id to broadcast to the correct guild only.
		serverID := resolveChannelServerID(store, channelID)

		msg, _ := json.Marshal(map[string]interface{}{
			"type":         "voice_state_update",
			"channel_id":   channelID,
			"participants": participants,
		})
		if serverID != "" {
			hub.BroadcastToServer(serverID, msg)
		} else {
			hub.BroadcastToAll(msg)
		}

		// When the last participant leaves, destroy the voice MLS group.
		// This enforces a clean forward-secrecy boundary between voice sessions:
		// the next session always starts a fresh group at epoch 0.
		if event.GetEvent() == webhook.EventParticipantLeft && len(participants) == 0 {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := store.DeleteMLSGroupInfo(ctx, channelID, "voice"); err != nil {
					slog.Warn("voice: failed to delete MLS group info on room empty", "channel", channelID, "err", err)
				} else {
					slog.Info("voice: deleted MLS group info (room empty)", "channel", channelID)
				}
			}()

			// Broadcast voice_group_destroyed so clients can clean up local MLS state.
			destroyMsg, _ := json.Marshal(map[string]interface{}{
				"type":       "voice_group_destroyed",
				"channel_id": channelID,
			})
			if serverID != "" {
				hub.BroadcastToServer(serverID, destroyMsg)
			} else {
				hub.BroadcastToAll(destroyMsg)
			}
		}

		w.WriteHeader(http.StatusOK)
	}
}

// resolveChannelServerID looks up the server_id for a channel.
// Returns empty string if the store is nil, the channel is not found, or the
// channel has no server association (fail-open: caller falls back to BroadcastToAll).
func resolveChannelServerID(store db.Store, channelID string) string {
	if store == nil || channelID == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), webhookChannelLookupTimeout)
	defer cancel()
	ch, err := store.GetChannelByID(ctx, channelID)
	if err != nil || ch == nil || ch.ServerID == nil {
		return ""
	}
	return *ch.ServerID
}

func participantDisplayName(p *livekit.ParticipantInfo) string {
	if p.GetName() != "" {
		return p.GetName()
	}
	return p.GetIdentity()
}
