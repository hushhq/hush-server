package ws

import (
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient creates a Client suitable for hub-only tests (no real WebSocket).
func newTestClient(hub *Hub, userID string) *Client {
	return NewClient(nil, hub, userID, nil)
}

// drainPresence reads from the client's send channel and returns the user_ids
// from the first presence.update message found within the timeout.
func drainPresence(t *testing.T, c *Client, timeout time.Duration) []string {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case raw := <-c.send:
			var msg struct {
				Type    string   `json:"type"`
				UserIDs []string `json:"user_ids"`
			}
			require.NoError(t, json.Unmarshal(raw, &msg))
			if msg.Type == "presence.update" {
				sort.Strings(msg.UserIDs)
				return msg.UserIDs
			}
		case <-deadline:
			t.Fatal("timed out waiting for presence.update")
			return nil
		}
	}
}

// drainAllMessages reads all pending messages from the send channel within the timeout.
func drainAllMessages(c *Client, timeout time.Duration) [][]byte {
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

func TestHub_RegisterClient_AddsToPresence(t *testing.T) {
	hub := NewHub()
	c := newTestClient(hub, "user-1")

	hub.Register(c)

	ids := drainPresence(t, c, 100*time.Millisecond)
	assert.Equal(t, []string{"user-1"}, ids)
}

func TestHub_UnregisterClient_RemovesFromPresence(t *testing.T) {
	hub := NewHub()
	c := newTestClient(hub, "user-1")
	observer := newTestClient(hub, "observer")

	hub.Register(observer)
	_ = drainAllMessages(observer, 50*time.Millisecond)

	hub.Register(c)
	_ = drainAllMessages(c, 50*time.Millisecond)
	_ = drainAllMessages(observer, 50*time.Millisecond)

	hub.Unregister(c)

	ids := drainPresence(t, observer, 100*time.Millisecond)
	assert.Equal(t, []string{"observer"}, ids)
}

func TestHub_MultipleSessionsSameUser_PresenceStaysOnPartialDisconnect(t *testing.T) {
	hub := NewHub()
	c1 := newTestClient(hub, "user-1")
	c2 := newTestClient(hub, "user-1")

	hub.Register(c1)
	hub.Register(c2)
	_ = drainAllMessages(c1, 50*time.Millisecond)
	_ = drainAllMessages(c2, 50*time.Millisecond)

	hub.Unregister(c1)

	ids := drainPresence(t, c2, 100*time.Millisecond)
	assert.Contains(t, ids, "user-1")
}

func TestHub_MultipleSessionsSameUser_PresenceRemovedOnFullDisconnect(t *testing.T) {
	hub := NewHub()
	observer := newTestClient(hub, "observer")
	hub.Register(observer)
	_ = drainAllMessages(observer, 50*time.Millisecond)

	c1 := newTestClient(hub, "user-1")
	c2 := newTestClient(hub, "user-1")
	hub.Register(c1)
	hub.Register(c2)
	_ = drainAllMessages(observer, 50*time.Millisecond)

	hub.Unregister(c1)
	_ = drainAllMessages(observer, 50*time.Millisecond)

	hub.Unregister(c2)

	ids := drainPresence(t, observer, 100*time.Millisecond)
	assert.Equal(t, []string{"observer"}, ids)
}

func TestHub_Subscribe_ClientReceivesBroadcast(t *testing.T) {
	hub := NewHub()
	c := newTestClient(hub, "user-1")
	hub.Register(c)
	_ = drainAllMessages(c, 50*time.Millisecond)

	hub.Subscribe(c, "channel-1")
	hub.Broadcast("channel-1", []byte(`{"msg":"hello"}`), "")

	select {
	case raw := <-c.send:
		assert.JSONEq(t, `{"msg":"hello"}`, string(raw))
	case <-time.After(100 * time.Millisecond):
		t.Fatal("client did not receive broadcast")
	}
}

func TestHub_Broadcast_ExcludesSender(t *testing.T) {
	hub := NewHub()
	sender := newTestClient(hub, "user-1")
	receiver := newTestClient(hub, "user-2")
	hub.Register(sender)
	hub.Register(receiver)
	_ = drainAllMessages(sender, 50*time.Millisecond)
	_ = drainAllMessages(receiver, 50*time.Millisecond)

	hub.Subscribe(sender, "channel-1")
	hub.Subscribe(receiver, "channel-1")
	hub.Broadcast("channel-1", []byte(`{"msg":"test"}`), sender.id)

	// Receiver should get the message.
	select {
	case raw := <-receiver.send:
		assert.JSONEq(t, `{"msg":"test"}`, string(raw))
	case <-time.After(100 * time.Millisecond):
		t.Fatal("receiver did not get broadcast")
	}

	// Sender should NOT get the message.
	select {
	case raw := <-sender.send:
		t.Fatalf("sender should not receive broadcast, got: %s", raw)
	case <-time.After(50 * time.Millisecond):
		// Expected: nothing received.
	}
}

func TestHub_Unsubscribe_ClientStopsReceiving(t *testing.T) {
	hub := NewHub()
	c := newTestClient(hub, "user-1")
	hub.Register(c)
	_ = drainAllMessages(c, 50*time.Millisecond)

	hub.Subscribe(c, "channel-1")
	hub.Unsubscribe(c, "channel-1")
	hub.Broadcast("channel-1", []byte(`{"msg":"nope"}`), "")

	select {
	case raw := <-c.send:
		t.Fatalf("client should not receive after unsubscribe, got: %s", raw)
	case <-time.After(50 * time.Millisecond):
		// Expected: nothing received.
	}
}

func TestHub_SubscribeMultipleChannels_IsolatedBroadcast(t *testing.T) {
	hub := NewHub()
	c1 := newTestClient(hub, "user-1")
	c2 := newTestClient(hub, "user-2")
	hub.Register(c1)
	hub.Register(c2)
	_ = drainAllMessages(c1, 50*time.Millisecond)
	_ = drainAllMessages(c2, 50*time.Millisecond)

	hub.Subscribe(c1, "channel-A")
	hub.Subscribe(c2, "channel-B")
	hub.Broadcast("channel-A", []byte(`{"ch":"A"}`), "")

	// c1 subscribed to channel-A should receive.
	select {
	case raw := <-c1.send:
		assert.JSONEq(t, `{"ch":"A"}`, string(raw))
	case <-time.After(100 * time.Millisecond):
		t.Fatal("c1 did not receive broadcast on channel-A")
	}

	// c2 subscribed to channel-B should NOT receive.
	select {
	case raw := <-c2.send:
		t.Fatalf("c2 should not receive channel-A broadcast, got: %s", raw)
	case <-time.After(50 * time.Millisecond):
		// Expected: nothing received.
	}
}

func TestHub_UnregisterClient_RemovedFromChannels(t *testing.T) {
	hub := NewHub()
	c := newTestClient(hub, "user-1")
	hub.Register(c)
	_ = drainAllMessages(c, 50*time.Millisecond)

	hub.Subscribe(c, "channel-1")
	hub.Unregister(c)

	// Broadcast to channel-1 should not panic and no one should receive.
	hub.Broadcast("channel-1", []byte(`{"msg":"ghost"}`), "")

	hub.mu.RLock()
	subs := hub.channels["channel-1"]
	hub.mu.RUnlock()
	assert.Empty(t, subs, "channel should have no subscribers after client unregistered")
}
