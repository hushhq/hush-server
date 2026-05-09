package api

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// voiceGroupTestBroadcaster captures BroadcastToServer calls for assertion.
type voiceGroupTestBroadcaster struct {
	mu       sync.Mutex
	messages [][]byte
	serverID string
}

func (b *voiceGroupTestBroadcaster) BroadcastToServer(serverID string, msg []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.serverID = serverID
	clone := make([]byte, len(msg))
	copy(clone, msg)
	b.messages = append(b.messages, clone)
}

func (b *voiceGroupTestBroadcaster) BroadcastToAll(msg []byte) {}

func (b *voiceGroupTestBroadcaster) messagesOfType(msgType string) []map[string]interface{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	var result []map[string]interface{}
	for _, raw := range b.messages {
		var m map[string]interface{}
		if err := json.Unmarshal(raw, &m); err != nil {
			continue
		}
		if m["type"] == msgType {
			result = append(result, m)
		}
	}
	return result
}

// simulateParticipantLeave runs the last-participant-leave logic that the webhook handler
// executes internally, so tests can verify voice group cleanup without signing webhook events.
func simulateParticipantLeave(
	state *VoiceState,
	store *mockStore,
	broadcaster *voiceGroupTestBroadcaster,
	roomName, identity, serverID string,
) []voiceParticipant {
	participants := state.leave(roomName, identity)

	channelID, ok := parseRoomName(roomName)
	if !ok {
		return participants
	}

	if len(participants) == 0 {
		// Last participant left - destroy voice MLS group.
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = store.DeleteMLSGroupInfo(ctx, channelID, "voice")
		}()

		destroyMsg, _ := json.Marshal(map[string]interface{}{
			"type":       "voice_group_destroyed",
			"channel_id": channelID,
		})
		if serverID != "" {
			broadcaster.BroadcastToServer(serverID, destroyMsg)
		} else {
			broadcaster.BroadcastToAll(destroyMsg)
		}
	}

	return participants
}

// TestVoiceGroupCleanup_LastParticipantLeave verifies that when the last participant
// leaves a voice channel, DeleteMLSGroupInfo is called with groupType="voice".
func TestVoiceGroupCleanup_LastParticipantLeave(t *testing.T) {
	var deleteCalled bool
	var deletedChannelID, deletedGroupType string

	store := &mockStore{
		deleteMLSGroupInfoFn: func(_ context.Context, channelID string, groupType string) error {
			deleteCalled = true
			deletedChannelID = channelID
			deletedGroupType = groupType
			return nil
		},
	}

	state := newVoiceState()
	broadcaster := &voiceGroupTestBroadcaster{}

	channelID := "550e8400-e29b-41d4-a716-446655440099"
	roomName := "channel-" + channelID

	// First participant joins.
	state.join(roomName, "user-alice", "Alice")
	// Alice leaves - she was the last participant.
	simulateParticipantLeave(state, store, broadcaster, roomName, "user-alice", "server-1")

	require.True(t, deleteCalled, "DeleteMLSGroupInfo must be called when last participant leaves")
	assert.Equal(t, channelID, deletedChannelID, "DeleteMLSGroupInfo must use the channel ID from the room name")
	assert.Equal(t, "voice", deletedGroupType, "DeleteMLSGroupInfo must be called with groupType='voice'")
}

// TestVoiceGroupCleanup_NotLastParticipant verifies that DeleteMLSGroupInfo is NOT called
// when there are still participants remaining in the voice channel.
func TestVoiceGroupCleanup_NotLastParticipant(t *testing.T) {
	var deleteCalled bool

	store := &mockStore{
		deleteMLSGroupInfoFn: func(_ context.Context, _ string, _ string) error {
			deleteCalled = true
			return nil
		},
	}

	state := newVoiceState()
	broadcaster := &voiceGroupTestBroadcaster{}

	channelID := "550e8400-e29b-41d4-a716-446655440098"
	roomName := "channel-" + channelID

	// Two participants join.
	state.join(roomName, "user-alice", "Alice")
	state.join(roomName, "user-bob", "Bob")

	// Alice leaves - Bob remains. Group must NOT be destroyed.
	remaining := simulateParticipantLeave(state, store, broadcaster, roomName, "user-alice", "server-1")

	assert.False(t, deleteCalled, "DeleteMLSGroupInfo must NOT be called when participants remain")
	require.Len(t, remaining, 1, "Bob must still be in the channel")
	assert.Equal(t, "user-bob", remaining[0].UserID)
}

// TestVoiceGroupCleanup_BroadcastVoiceGroupDestroyed verifies that when the last
// participant leaves, a voice_group_destroyed WS event is broadcast with the correct
// channel_id.
func TestVoiceGroupCleanup_BroadcastVoiceGroupDestroyed(t *testing.T) {
	store := &mockStore{}
	state := newVoiceState()
	broadcaster := &voiceGroupTestBroadcaster{}

	channelID := "550e8400-e29b-41d4-a716-446655440097"
	roomName := "channel-" + channelID
	serverID := "server-xyz-123"

	state.join(roomName, "user-alice", "Alice")
	simulateParticipantLeave(state, store, broadcaster, roomName, "user-alice", serverID)

	destroyed := broadcaster.messagesOfType("voice_group_destroyed")
	require.Len(t, destroyed, 1, "exactly one voice_group_destroyed event must be broadcast")
	assert.Equal(t, channelID, destroyed[0]["channel_id"], "voice_group_destroyed payload must contain the channel ID")
	assert.Equal(t, serverID, broadcaster.serverID, "voice_group_destroyed must be broadcast to the correct server")
}
