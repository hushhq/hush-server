package ws

import (
	"context"
	"encoding/json"
	"errors"
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

func TestClient_SubscribeChannel_AllowsChannelMember(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		isChannelMemberFn: func(_ctx context.Context, channelID, userID string) (bool, error) {
			if channelID != "channel-1" || userID != "user-1" {
				t.Fatalf("membership checked with channelID=%q userID=%q", channelID, userID)
			}
			return true, nil
		},
	}
	handler := NewMessageHandler(store, hub)
	client := NewClient(nil, hub, "user-1", "device-1", "", handler)

	client.handleMessage([]byte(`{"type":"subscribe","channel_id":"channel-1"}`))

	hub.mu.RLock()
	_, isSubscribed := hub.channels["channel-1"][client.id]
	hub.mu.RUnlock()
	if !isSubscribed {
		t.Fatal("expected channel member to be subscribed")
	}
}

func TestClient_SubscribeChannel_RejectsNonMember(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		isChannelMemberFn: func(_ctx context.Context, channelID, userID string) (bool, error) {
			if channelID != "channel-1" || userID != "user-1" {
				t.Fatalf("membership checked with channelID=%q userID=%q", channelID, userID)
			}
			return false, nil
		},
	}
	handler := NewMessageHandler(store, hub)
	client := NewClient(nil, hub, "user-1", "device-1", "", handler)

	client.handleMessage([]byte(`{"type":"subscribe","channel_id":"channel-1"}`))

	hub.mu.RLock()
	_, isSubscribed := hub.channels["channel-1"][client.id]
	hub.mu.RUnlock()
	if isSubscribed {
		t.Fatal("expected non-member not to be subscribed")
	}

	messages := drainAllClientMessages(client, 20*time.Millisecond)
	if !hasErrorCode(messages, "forbidden") {
		t.Fatalf("expected forbidden error, got %q", messages)
	}
}

func TestClient_SubscribeServer_AllowsServerMember(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		getServerMemberLevelFn: func(_ctx context.Context, serverID, userID string) (int, error) {
			if serverID != "server-1" || userID != "user-1" {
				t.Fatalf("membership checked with serverID=%q userID=%q", serverID, userID)
			}
			return 1, nil
		},
	}
	handler := NewMessageHandler(store, hub)
	client := NewClient(nil, hub, "user-1", "device-1", "", handler)

	client.handleMessage([]byte(`{"type":"subscribe.server","server_id":"server-1"}`))

	hub.mu.RLock()
	_, isSubscribed := hub.servers["server-1"][client.id]
	hub.mu.RUnlock()
	if !isSubscribed {
		t.Fatal("expected server member to be subscribed")
	}
}

func TestClient_SubscribeServer_RejectsNonMember(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		getServerMemberLevelFn: func(_ctx context.Context, serverID, userID string) (int, error) {
			if serverID != "server-1" || userID != "user-1" {
				t.Fatalf("membership checked with serverID=%q userID=%q", serverID, userID)
			}
			return 0, errors.New("not a member")
		},
	}
	handler := NewMessageHandler(store, hub)
	client := NewClient(nil, hub, "user-1", "device-1", "", handler)

	client.handleMessage([]byte(`{"type":"subscribe.server","server_id":"server-1"}`))

	hub.mu.RLock()
	_, isSubscribed := hub.servers["server-1"][client.id]
	hub.mu.RUnlock()
	if isSubscribed {
		t.Fatal("expected non-member not to be subscribed")
	}

	messages := drainAllClientMessages(client, 20*time.Millisecond)
	if !hasErrorCode(messages, "forbidden") {
		t.Fatalf("expected forbidden error, got %q", messages)
	}
}

func hasErrorCode(messages [][]byte, expectedCode string) bool {
	for _, raw := range messages {
		var msg struct {
			Type string `json:"type"`
			Code string `json:"code"`
		}
		if err := json.Unmarshal(raw, &msg); err == nil && msg.Type == "error" && msg.Code == expectedCode {
			return true
		}
	}
	return false
}
