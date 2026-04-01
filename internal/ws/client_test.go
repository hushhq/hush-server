package ws

import (
	"encoding/json"
	"testing"
	"time"
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

// TestClient_MediaKey_RemovedInM3 verifies that media.key messages are silently
// ignored after M.3-01 removal. Frame keys are now derived locally via MLS
// export_secret - no key material travels over the wire.
func TestClient_MediaKey_RemovedInM3(t *testing.T) {
	hub := NewHub()
	sender := NewClient(nil, hub, "user-1", "device-1", "", nil)
	receiver := NewClient(nil, hub, "user-2", "device-2", "", nil)
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
		"type":           "media.key",
		"target_user_id": "user-2",
		"payload":        "cGF5bG9hZA==",
	})
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}

	sender.handleMessage(raw)

	select {
	case msg := <-receiver.send:
		t.Fatalf("media.key handler was removed in M.3-01 - no message should be relayed, got: %s", string(msg))
	case <-time.After(100 * time.Millisecond):
		// Expected: media.key is silently ignored (handler removed).
	}
}
