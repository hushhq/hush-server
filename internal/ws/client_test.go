package ws

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainAllClientMessages reads all pending messages from the client's send channel within the timeout.
func drainAllClientMessages(c *Client, timeout time.Duration) [][]byte {
	var msgs [][]byte
	deadline := time.After(timeout)
	for {
		select {
		case raw := <-c.send:
			msgs = append(msgs, raw)
		case <-deadline:
			return msgs
		}
	}
}

func TestClient_HandleMediaKey_RelaysToTargetUser(t *testing.T) {
	hub := NewHub()
	sender := NewClient(nil, hub, "user-1", nil)
	receiver := NewClient(nil, hub, "user-2", nil)
	hub.Register(sender)
	hub.Register(receiver)
	defer func() {
		hub.Unregister(sender)
		hub.Unregister(receiver)
		close(sender.send)
		close(receiver.send)
	}()

	// Drain initial presence updates.
	_ = drainAllClientMessages(sender, 50*time.Millisecond)
	_ = drainAllClientMessages(receiver, 50*time.Millisecond)

	raw, err := json.Marshal(map[string]string{
		"type":          "media.key",
		"target_user_id": "user-2",
		"payload":       "cGF5bG9hZA==",
	})
	require.NoError(t, err)

	sender.handleMessage(raw)

	select {
	case msg := <-receiver.send:
		var out struct {
			Type         string `json:"type"`
			SenderUserID string `json:"sender_user_id"`
			Payload      string `json:"payload"`
		}
		require.NoError(t, json.Unmarshal(msg, &out))
		assert.Equal(t, "media.key", out.Type)
		assert.Equal(t, "user-1", out.SenderUserID)
		assert.Equal(t, "cGF5bG9hZA==", out.Payload)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for media.key on receiver")
	}
}

func TestClient_HandleMediaKey_MissingTargetUserIgnored(t *testing.T) {
	hub := NewHub()
	sender := NewClient(nil, hub, "user-1", nil)
	receiver := NewClient(nil, hub, "user-2", nil)
	hub.Register(sender)
	hub.Register(receiver)
	defer func() {
		hub.Unregister(sender)
		hub.Unregister(receiver)
		close(sender.send)
		close(receiver.send)
	}()

	// Drain initial presence updates.
	_ = drainAllClientMessages(sender, 50*time.Millisecond)
	_ = drainAllClientMessages(receiver, 50*time.Millisecond)

	raw, err := json.Marshal(map[string]string{
		"type":    "media.key",
		"payload": "ignored",
	})
	require.NoError(t, err)

	sender.handleMessage(raw)

	select {
	case msg := <-receiver.send:
		t.Fatalf("expected no message for receiver, got: %s", string(msg))
	case <-time.After(100 * time.Millisecond):
		// Expected: no messages relayed.
	}
}

