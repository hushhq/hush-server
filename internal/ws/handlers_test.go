package ws

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"hush.app/server/internal/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// messageStoreMock implements db.Store for message handler tests. Only message methods are used.
type messageStoreMock struct {
	insertMessageFn   func(ctx context.Context, channelID, senderID string, ciphertext []byte) (*models.Message, error)
	getMessagesFn     func(ctx context.Context, channelID string, before time.Time, limit int) ([]models.Message, error)
	isChannelMemberFn func(ctx context.Context, channelID, userID string) (bool, error)
}

func (m *messageStoreMock) CreateUser(context.Context, string, string, *string) (*models.User, error)              { return nil, nil }
func (m *messageStoreMock) GetUserByUsername(context.Context, string) (*models.User, error)                         { return nil, nil }
func (m *messageStoreMock) GetUserByID(context.Context, string) (*models.User, error)                               { return nil, nil }
func (m *messageStoreMock) CreateSession(context.Context, string, string, string, time.Time) (*models.Session, error) { return nil, nil }
func (m *messageStoreMock) GetSessionByTokenHash(context.Context, string) (*models.Session, error)                   { return nil, nil }
func (m *messageStoreMock) DeleteSessionByID(context.Context, string) error                                         { return nil }
func (m *messageStoreMock) UpsertIdentityKeys(context.Context, string, string, []byte, []byte, []byte, int) error   { return nil }
func (m *messageStoreMock) InsertOneTimePreKeys(context.Context, string, string, []models.OneTimePreKeyRow) error  { return nil }
func (m *messageStoreMock) GetIdentityAndSignedPreKey(context.Context, string, string) ([]byte, []byte, []byte, int, error) {
	return nil, nil, nil, 0, nil
}
func (m *messageStoreMock) ConsumeOneTimePreKey(context.Context, string, string) (int, []byte, error)     { return 0, nil, nil }
func (m *messageStoreMock) CountUnusedOneTimePreKeys(context.Context, string, string) (int, error)          { return 0, nil }
func (m *messageStoreMock) ListDeviceIDsForUser(context.Context, string) ([]string, error)                 { return nil, nil }
func (m *messageStoreMock) UpsertDevice(context.Context, string, string, string) error                     { return nil }

func (m *messageStoreMock) InsertMessage(ctx context.Context, channelID, senderID string, ciphertext []byte) (*models.Message, error) {
	if m.insertMessageFn != nil {
		return m.insertMessageFn(ctx, channelID, senderID, ciphertext)
	}
	return nil, nil
}
func (m *messageStoreMock) GetMessages(ctx context.Context, channelID string, before time.Time, limit int) ([]models.Message, error) {
	if m.getMessagesFn != nil {
		return m.getMessagesFn(ctx, channelID, before, limit)
	}
	return nil, nil
}
func (m *messageStoreMock) IsChannelMember(ctx context.Context, channelID, userID string) (bool, error) {
	if m.isChannelMemberFn != nil {
		return m.isChannelMemberFn(ctx, channelID, userID)
	}
	return false, nil
}

// drainUntilType reads from c.send until a message with the given type is received or timeout.
func drainUntilType(t *testing.T, c *Client, wantType string, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-c.send:
			var out struct{ Type string `json:"type"` }
			if err := json.Unmarshal(msg, &out); err != nil {
				continue
			}
			if out.Type == wantType {
				return msg
			}
		case <-deadline:
			t.Fatalf("timed out waiting for type %q", wantType)
			return nil
		}
	}
}

func TestMessageHandler_HandleMessageSend_ForbiddenWhenNotMember(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		isChannelMemberFn: func(ctx context.Context, channelID, userID string) (bool, error) {
			return false, nil
		},
	}
	h := NewMessageHandler(store, hub)
	c := NewClient(nil, hub, "user1", h)
	hub.Register(c)
	defer func() { hub.Unregister(c); close(c.send) }()

	raw, _ := json.Marshal(map[string]string{"channel_id": "ch1", "ciphertext": "YWVz"})
	h.Handle(c, "message.send", raw)

	msg := drainUntilType(t, c, "error", time.Second)
	var out struct {
		Type    string `json:"type"`
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(msg, &out))
	assert.Equal(t, "error", out.Type)
	assert.Equal(t, "forbidden", out.Code)
}

func TestMessageHandler_HandleMessageSend_StoresAndBroadcasts(t *testing.T) {
	hub := NewHub()
	var inserted *models.Message
	store := &messageStoreMock{
		isChannelMemberFn: func(ctx context.Context, channelID, userID string) (bool, error) { return true, nil },
		insertMessageFn: func(ctx context.Context, channelID, senderID string, ciphertext []byte) (*models.Message, error) {
			inserted = &models.Message{
				ID:         "msg-1",
				ChannelID:  channelID,
				SenderID:   senderID,
				Ciphertext: ciphertext,
				Timestamp:  time.Now(),
			}
			return inserted, nil
		},
	}
	h := NewMessageHandler(store, hub)
	sender := NewClient(nil, hub, "user1", h)
	hub.Register(sender)
	recv := NewClient(nil, hub, "user2", nil)
	hub.Register(recv)
	hub.Subscribe(sender, "ch1")
	hub.Subscribe(recv, "ch1")
	defer func() {
		hub.Unregister(sender)
		hub.Unregister(recv)
		close(sender.send)
		close(recv.send)
	}()

	raw, _ := json.Marshal(map[string]string{"channel_id": "ch1", "ciphertext": "YWVz"})
	h.Handle(sender, "message.send", raw)

	require.NotNil(t, inserted)
	assert.Equal(t, "ch1", inserted.ChannelID)
	assert.Equal(t, "user1", inserted.SenderID)

	msg := drainUntilType(t, recv, "message.new", time.Second)
	{
		var out struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			ChannelID string `json:"channel_id"`
			SenderID  string `json:"sender_id"`
		}
		require.NoError(t, json.Unmarshal(msg, &out))
		assert.Equal(t, "message.new", out.Type)
		assert.Equal(t, "msg-1", out.ID)
		assert.Equal(t, "ch1", out.ChannelID)
		assert.Equal(t, "user1", out.SenderID)
	}
}

func TestMessageHandler_HandleMessageHistory_ForbiddenWhenNotMember(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		isChannelMemberFn: func(ctx context.Context, channelID, userID string) (bool, error) { return false, nil },
	}
	h := NewMessageHandler(store, hub)
	c := NewClient(nil, hub, "user1", h)
	hub.Register(c)
	defer func() { hub.Unregister(c); close(c.send) }()

	raw, _ := json.Marshal(map[string]string{"channel_id": "ch1"})
	h.Handle(c, "message.history", raw)

	msg := drainUntilType(t, c, "error", time.Second)
	var out struct{ Type, Code string }
	require.NoError(t, json.Unmarshal(msg, &out))
	assert.Equal(t, "error", out.Type)
	assert.Equal(t, "forbidden", out.Code)
}

func TestMessageHandler_HandleMessageHistory_ReturnsMessages(t *testing.T) {
	hub := NewHub()
	msgs := []models.Message{
		{ID: "m1", ChannelID: "ch1", SenderID: "u1", Ciphertext: []byte("a"), Timestamp: time.Now()},
	}
	store := &messageStoreMock{
		isChannelMemberFn: func(ctx context.Context, channelID, userID string) (bool, error) { return true, nil },
		getMessagesFn:     func(ctx context.Context, channelID string, before time.Time, limit int) ([]models.Message, error) { return msgs, nil },
	}
	h := NewMessageHandler(store, hub)
	c := NewClient(nil, hub, "user1", h)
	hub.Register(c)
	defer func() { hub.Unregister(c); close(c.send) }()

	raw, _ := json.Marshal(map[string]string{"channel_id": "ch1"})
	h.Handle(c, "message.history", raw)

	msg := drainUntilType(t, c, "message.history.response", time.Second)
	var resp struct {
		Type     string `json:"type"`
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(msg, &resp))
	assert.Equal(t, "message.history.response", resp.Type)
	require.Len(t, resp.Messages, 1)
	assert.Equal(t, "m1", resp.Messages[0].ID)
}

func TestMessageHandler_HandleTyping_BroadcastsToChannel(t *testing.T) {
	hub := NewHub()
	store := &messageStoreMock{
		isChannelMemberFn: func(ctx context.Context, channelID, userID string) (bool, error) { return true, nil },
	}
	h := NewMessageHandler(store, hub)
	c := NewClient(nil, hub, "user1", h)
	hub.Register(c)
	other := NewClient(nil, hub, "user2", nil)
	hub.Register(other)
	hub.Subscribe(c, "ch1")
	hub.Subscribe(other, "ch1")
	defer func() {
		hub.Unregister(c)
		hub.Unregister(other)
		close(c.send)
		close(other.send)
	}()

	raw, _ := json.Marshal(map[string]string{"channel_id": "ch1"})
	h.Handle(c, "typing.start", raw)

	msg := drainUntilType(t, other, "typing.start", time.Second)
	var out struct {
		Type      string `json:"type"`
		ChannelID string `json:"channel_id"`
		UserID    string `json:"user_id"`
	}
	require.NoError(t, json.Unmarshal(msg, &out))
	assert.Equal(t, "typing.start", out.Type)
	assert.Equal(t, "ch1", out.ChannelID)
	assert.Equal(t, "user1", out.UserID)
}
