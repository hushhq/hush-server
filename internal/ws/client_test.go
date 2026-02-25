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

func TestClient_HandleMediaKey_SelfRelayBlocked(t *testing.T) {
	hub := NewHub()
	sender := NewClient(nil, hub, "user-1", nil)
	hub.Register(sender)
	defer func() {
		hub.Unregister(sender)
		close(sender.send)
	}()

	_ = drainAllClientMessages(sender, 50*time.Millisecond)

	raw, err := json.Marshal(map[string]string{
		"type":           "media.key",
		"target_user_id": "user-1",
		"payload":        "c2VsZi1yZWxheQ==",
	})
	require.NoError(t, err)

	sender.handleMessage(raw)

	select {
	case msg := <-sender.send:
		t.Fatalf("expected no self-relay, got: %s", string(msg))
	case <-time.After(100 * time.Millisecond):
		// Expected: self-relay blocked.
	}
}

func TestClient_HandleMediaKey_OversizedPayloadIgnored(t *testing.T) {
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

	_ = drainAllClientMessages(sender, 50*time.Millisecond)
	_ = drainAllClientMessages(receiver, 50*time.Millisecond)

	// Create a payload larger than maxMediaKeyPayload (4096 bytes).
	bigPayload := make([]byte, maxMediaKeyPayload+1)
	for i := range bigPayload {
		bigPayload[i] = 'A'
	}
	raw, err := json.Marshal(map[string]string{
		"type":           "media.key",
		"target_user_id": "user-2",
		"payload":        string(bigPayload),
	})
	require.NoError(t, err)

	sender.handleMessage(raw)

	select {
	case msg := <-receiver.send:
		t.Fatalf("expected no message for oversized payload, got: %s", string(msg))
	case <-time.After(100 * time.Millisecond):
		// Expected: oversized payload ignored.
	}
}

func TestClient_HandleMediaKey_NonexistentTargetNoOp(t *testing.T) {
	hub := NewHub()
	sender := NewClient(nil, hub, "user-1", nil)
	hub.Register(sender)
	defer func() {
		hub.Unregister(sender)
		close(sender.send)
	}()

	_ = drainAllClientMessages(sender, 50*time.Millisecond)

	raw, err := json.Marshal(map[string]string{
		"type":           "media.key",
		"target_user_id": "user-999",
		"payload":        "cGF5bG9hZA==",
	})
	require.NoError(t, err)

	// Should not panic or deadlock — simply a no-op.
	sender.handleMessage(raw)

	select {
	case msg := <-sender.send:
		t.Fatalf("expected no message for sender, got: %s", string(msg))
	case <-time.After(100 * time.Millisecond):
		// Expected: no-op for nonexistent target.
	}
}

