package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseRoomName(t *testing.T) {
	tests := []struct {
		name      string
		roomName  string
		serverID  string
		channelID string
		ok        bool
	}{
		{"valid", "server-abc123-channel-def456", "abc123", "def456", true},
		{"uuid ids", "server-550e8400-e29b-41d4-a716-446655440000-channel-660e8400-e29b-41d4-a716-446655440001", "550e8400-e29b-41d4-a716-446655440000", "660e8400-e29b-41d4-a716-446655440001", true},
		{"missing prefix", "room-abc-channel-def", "", "", false},
		{"missing channel sep", "server-abc-def", "", "", false},
		{"empty server id", "server--channel-def", "", "", false},
		{"empty channel id", "server-abc-channel-", "", "", false},
		{"empty string", "", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sid, cid, ok := parseRoomName(tt.roomName)
			assert.Equal(t, tt.ok, ok)
			if ok {
				assert.Equal(t, tt.serverID, sid)
				assert.Equal(t, tt.channelID, cid)
			}
		})
	}
}

func TestVoiceState_JoinLeave(t *testing.T) {
	state := newVoiceState()

	participants := state.join("room1", "user-a", "Alice")
	assert.Len(t, participants, 1)
	assert.Equal(t, "user-a", participants[0].UserID)
	assert.Equal(t, "Alice", participants[0].DisplayName)

	participants = state.join("room1", "user-b", "Bob")
	assert.Len(t, participants, 2)

	participants = state.leave("room1", "user-a")
	assert.Len(t, participants, 1)
	assert.Equal(t, "user-b", participants[0].UserID)

	participants = state.leave("room1", "user-b")
	assert.Len(t, participants, 0)

	// Room should be cleaned up from internal map.
	state.mu.RLock()
	_, exists := state.rooms["room1"]
	state.mu.RUnlock()
	assert.False(t, exists)
}

func TestVoiceState_LeaveNonexistent(t *testing.T) {
	state := newVoiceState()
	participants := state.leave("room-doesnt-exist", "user-a")
	assert.Len(t, participants, 0)
}

func TestVoiceState_DuplicateJoin(t *testing.T) {
	state := newVoiceState()
	state.join("room1", "user-a", "Alice")
	participants := state.join("room1", "user-a", "Alice Updated")
	assert.Len(t, participants, 1)
	assert.Equal(t, "Alice Updated", participants[0].DisplayName)
}
