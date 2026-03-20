package ws

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"hush.app/server/internal/db"
	"hush.app/server/internal/models"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// compile-time interface check: messageStoreMock must satisfy db.Store.
var _ db.Store = (*messageStoreMock)(nil)

// messageStoreMock implements db.Store for message handler tests. Only message methods are used.
type messageStoreMock struct {
	insertMessageFn   func(ctx context.Context, channelID, senderID string, recipientID *string, ciphertext []byte) (*models.Message, error)
	getMessagesFn     func(ctx context.Context, channelID, recipientID string, before time.Time, limit int) ([]models.Message, error)
	isChannelMemberFn func(ctx context.Context, channelID, userID string) (bool, error)
}

// User/session stubs (unused in ws handler tests).
func (m *messageStoreMock) CreateUser(context.Context, string, string, *string) (*models.User, error) {
	return nil, nil
}
func (m *messageStoreMock) GetUserByUsername(context.Context, string) (*models.User, error) {
	return nil, nil
}
func (m *messageStoreMock) GetUserByID(context.Context, string) (*models.User, error) {
	return nil, nil
}
func (m *messageStoreMock) CreateSession(context.Context, string, string, string, time.Time) (*models.Session, error) {
	return nil, nil
}
func (m *messageStoreMock) GetSessionByTokenHash(context.Context, string) (*models.Session, error) {
	return nil, nil
}
func (m *messageStoreMock) DeleteSessionByID(context.Context, string) error { return nil }

// MLS credential stubs.
func (m *messageStoreMock) UpsertMLSCredential(context.Context, string, string, []byte, []byte, int) error {
	return nil
}
func (m *messageStoreMock) GetMLSCredential(context.Context, string, string) ([]byte, []byte, int, error) {
	return nil, nil, 0, nil
}

// MLS key package stubs.
func (m *messageStoreMock) InsertMLSKeyPackages(context.Context, string, string, [][]byte, time.Time) error {
	return nil
}
func (m *messageStoreMock) InsertMLSLastResortKeyPackage(context.Context, string, string, []byte) error {
	return nil
}
func (m *messageStoreMock) ConsumeMLSKeyPackage(context.Context, string, string) ([]byte, error) {
	return nil, nil
}
func (m *messageStoreMock) CountUnusedMLSKeyPackages(context.Context, string, string) (int, error) {
	return 0, nil
}
func (m *messageStoreMock) PurgeExpiredMLSKeyPackages(context.Context) (int64, error) { return 0, nil }

// Device enumeration stubs.
func (m *messageStoreMock) ListDeviceIDsForUser(context.Context, string) ([]string, error) {
	return nil, nil
}
func (m *messageStoreMock) UpsertDevice(context.Context, string, string, string) error { return nil }

// Message methods (actually used).
func (m *messageStoreMock) InsertMessage(ctx context.Context, channelID, senderID string, recipientID *string, ciphertext []byte) (*models.Message, error) {
	if m.insertMessageFn != nil {
		return m.insertMessageFn(ctx, channelID, senderID, recipientID, ciphertext)
	}
	return nil, nil
}
func (m *messageStoreMock) GetMessages(ctx context.Context, channelID, recipientID string, before time.Time, limit int) ([]models.Message, error) {
	if m.getMessagesFn != nil {
		return m.getMessagesFn(ctx, channelID, recipientID, before, limit)
	}
	return nil, nil
}
func (m *messageStoreMock) IsChannelMember(ctx context.Context, channelID, userID string) (bool, error) {
	if m.isChannelMemberFn != nil {
		return m.isChannelMemberFn(ctx, channelID, userID)
	}
	return false, nil
}

// Instance stubs.
func (m *messageStoreMock) GetInstanceConfig(context.Context) (*models.InstanceConfig, error) {
	return nil, nil
}
func (m *messageStoreMock) UpdateInstanceConfig(context.Context, *string, *string, *string, *string) error {
	return nil
}
func (m *messageStoreMock) SetInstanceOwner(context.Context, string) (bool, error) { return false, nil }
func (m *messageStoreMock) GetUserRole(context.Context, string) (string, error)    { return "member", nil }
func (m *messageStoreMock) UpdateUserRole(context.Context, string, string) error   { return nil }
func (m *messageStoreMock) ListMembers(context.Context) ([]models.Member, error)   { return nil, nil }

// Channel stubs (guild-scoped — serverID param).
func (m *messageStoreMock) CreateChannel(context.Context, string, string, string, *string, *string, int) (*models.Channel, error) {
	return nil, nil
}
func (m *messageStoreMock) ListChannels(context.Context, string) ([]models.Channel, error) {
	return nil, nil
}
func (m *messageStoreMock) GetChannelByID(context.Context, string) (*models.Channel, error) {
	return nil, nil
}
func (m *messageStoreMock) GetChannelByNameAndType(context.Context, string, string, string) (*models.Channel, error) {
	return nil, nil
}
func (m *messageStoreMock) DeleteChannel(context.Context, string) error             { return nil }
func (m *messageStoreMock) MoveChannel(context.Context, string, *string, int) error { return nil }
func (m *messageStoreMock) ListServerTemplates(context.Context) ([]models.ServerTemplate, error) {
	return nil, nil
}
func (m *messageStoreMock) GetServerTemplateByID(context.Context, string) (*models.ServerTemplate, error) {
	return nil, nil
}
func (m *messageStoreMock) GetDefaultServerTemplate(context.Context) (*models.ServerTemplate, error) {
	return nil, nil
}
func (m *messageStoreMock) CreateServerTemplate(context.Context, string, json.RawMessage, bool) (*models.ServerTemplate, error) {
	return nil, nil
}
func (m *messageStoreMock) UpdateServerTemplate(context.Context, string, string, json.RawMessage, bool) error {
	return nil
}
func (m *messageStoreMock) DeleteServerTemplate(context.Context, string) error { return nil }

// Invite stubs (guild-scoped — serverID param).
func (m *messageStoreMock) CreateInvite(context.Context, string, string, string, int, time.Time) (*models.InviteCode, error) {
	return nil, nil
}
func (m *messageStoreMock) GetInviteByCode(context.Context, string) (*models.InviteCode, error) {
	return nil, nil
}
func (m *messageStoreMock) ClaimInviteUse(context.Context, string) (bool, error) { return true, nil }

// Server / guild operation stubs.
func (m *messageStoreMock) CreateServer(context.Context, string, string) (*models.Server, error) {
	return nil, nil
}
func (m *messageStoreMock) GetServerByID(context.Context, string) (*models.Server, error) {
	return nil, nil
}
func (m *messageStoreMock) ListServersForUser(context.Context, string) ([]models.Server, error) {
	return nil, nil
}
func (m *messageStoreMock) DeleteServer(context.Context, string) error { return nil }
func (m *messageStoreMock) ListGuildBillingStats(context.Context) ([]models.GuildBillingStats, error) {
	return nil, nil
}

// Server member operation stubs.
func (m *messageStoreMock) AddServerMember(context.Context, string, string, string) error { return nil }
func (m *messageStoreMock) RemoveServerMember(context.Context, string, string) error      { return nil }
func (m *messageStoreMock) GetServerMemberRole(context.Context, string, string) (string, error) {
	return "", nil
}
func (m *messageStoreMock) UpdateServerMemberRole(context.Context, string, string, string) error {
	return nil
}
func (m *messageStoreMock) ListServerMembers(context.Context, string) ([]models.ServerMemberWithUser, error) {
	return nil, nil
}

// Moderation stubs (guild-scoped — serverID param).
func (m *messageStoreMock) InsertBan(context.Context, string, string, string, string, *time.Time) (*models.Ban, error) {
	return nil, nil
}
func (m *messageStoreMock) GetActiveBan(context.Context, string, string) (*models.Ban, error) {
	return nil, nil
}
func (m *messageStoreMock) LiftBan(context.Context, string, string) error { return nil }
func (m *messageStoreMock) ListActiveBans(context.Context, string) ([]models.Ban, error) {
	return nil, nil
}
func (m *messageStoreMock) InsertMute(context.Context, string, string, string, string, *time.Time) (*models.Mute, error) {
	return nil, nil
}
func (m *messageStoreMock) GetActiveMute(context.Context, string, string) (*models.Mute, error) {
	return nil, nil
}
func (m *messageStoreMock) LiftMute(context.Context, string, string) error { return nil }
func (m *messageStoreMock) ListActiveMutes(context.Context, string) ([]models.Mute, error) {
	return nil, nil
}
func (m *messageStoreMock) InsertAuditLog(context.Context, string, string, *string, string, string, map[string]interface{}) error {
	return nil
}
func (m *messageStoreMock) ListAuditLog(_ context.Context, _ string, _, _ int, _ *db.AuditLogFilter) ([]models.AuditLogEntry, error) {
	return nil, nil
}
func (m *messageStoreMock) GetMessageByID(context.Context, string) (*models.Message, error) {
	return nil, nil
}
func (m *messageStoreMock) DeleteMessage(context.Context, string) error          { return nil }
func (m *messageStoreMock) DeleteSessionsByUserID(context.Context, string) error { return nil }

// Instance ban stubs.
func (m *messageStoreMock) InsertInstanceBan(context.Context, string, string, string, *time.Time) (*models.InstanceBan, error) {
	return nil, nil
}
func (m *messageStoreMock) GetActiveInstanceBan(context.Context, string) (*models.InstanceBan, error) {
	return nil, nil
}
func (m *messageStoreMock) LiftInstanceBan(context.Context, string, string) error { return nil }

// Instance audit log stubs.
func (m *messageStoreMock) InsertInstanceAuditLog(context.Context, string, *string, string, string, map[string]interface{}) error {
	return nil
}
func (m *messageStoreMock) ListInstanceAuditLog(_ context.Context, _, _ int, _ *db.InstanceAuditLogFilter) ([]models.InstanceAuditLogEntry, error) {
	return nil, nil
}

// User search stub.
func (m *messageStoreMock) SearchUsers(context.Context, string, int) ([]models.UserSearchResult, error) {
	return nil, nil
}

// System messages stubs.
func (m *messageStoreMock) InsertSystemMessage(context.Context, string, string, string, *string, string, map[string]interface{}) (*models.SystemMessage, error) {
	return nil, nil
}
func (m *messageStoreMock) ListSystemMessages(context.Context, string, time.Time, int) ([]models.SystemMessage, error) {
	return nil, nil
}
func (m *messageStoreMock) PurgeExpiredSystemMessages(context.Context, int) (int64, error) {
	return 0, nil
}
func (m *messageStoreMock) GetSystemMessageRetentionDays(context.Context) (*int, error) {
	return nil, nil
}

// MLS group stubs.
func (m *messageStoreMock) UpsertMLSGroupInfo(context.Context, string, []byte, int64) error {
	return nil
}
func (m *messageStoreMock) GetMLSGroupInfo(context.Context, string) ([]byte, int64, error) {
	return nil, 0, nil
}
func (m *messageStoreMock) AppendMLSCommit(context.Context, string, int64, []byte, string) error {
	return nil
}
func (m *messageStoreMock) GetMLSCommitsSinceEpoch(context.Context, string, int64, int) ([]db.MLSCommitRow, error) {
	return nil, nil
}
func (m *messageStoreMock) DeleteMLSGroupInfo(context.Context, string) error { return nil }
func (m *messageStoreMock) PurgeOldMLSCommits(context.Context, int) (int64, error) {
	return 0, nil
}
func (m *messageStoreMock) StorePendingWelcome(context.Context, string, string, string, []byte, int64) error {
	return nil
}
func (m *messageStoreMock) GetPendingWelcomes(context.Context, string) ([]db.PendingWelcomeRow, error) {
	return nil, nil
}
func (m *messageStoreMock) DeletePendingWelcome(context.Context, string) error { return nil }

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
		insertMessageFn: func(ctx context.Context, channelID, senderID string, recipientID *string, ciphertext []byte) (*models.Message, error) {
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
		getMessagesFn:     func(ctx context.Context, channelID, recipientID string, before time.Time, limit int) ([]models.Message, error) { return msgs, nil },
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

func TestMessageHandler_HandleMessageSend_FanoutStoresAndBroadcastsPerRecipient(t *testing.T) {
	hub := NewHub()
	var insertCount int
	var lastRecipientID *string
	store := &messageStoreMock{
		isChannelMemberFn: func(context.Context, string, string) (bool, error) { return true, nil },
		insertMessageFn: func(ctx context.Context, channelID, senderID string, recipientID *string, ciphertext []byte) (*models.Message, error) {
			insertCount++
			lastRecipientID = recipientID
			return &models.Message{ID: "msg-" + *recipientID, ChannelID: channelID, SenderID: senderID, Ciphertext: ciphertext, Timestamp: time.Now()}, nil
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

	raw, _ := json.Marshal(map[string]interface{}{
		"channel_id":              "ch1",
		"ciphertext_by_recipient": map[string]string{"user2": "YWVz", "user3": "eHl6"},
	})
	h.Handle(sender, "message.send", raw)

	// 2 recipient inserts + 1 sender copy = 3
	assert.Equal(t, 3, insertCount)
	// Last insert is the sender copy (recipient_id = sender)
	assert.NotNil(t, lastRecipientID)
	assert.Equal(t, "user1", *lastRecipientID)

	// user2 should receive only their own ciphertext, not user3's
	msg := drainUntilType(t, recv, "message.new", time.Second)
	var out struct {
		Type       string `json:"type"`
		ID         string `json:"id"`
		ChannelID  string `json:"channel_id"`
		SenderID   string `json:"sender_id"`
		Ciphertext string `json:"ciphertext"`
	}
	require.NoError(t, json.Unmarshal(msg, &out))
	assert.Equal(t, "message.new", out.Type)
	assert.Equal(t, "ch1", out.ChannelID)
	assert.Equal(t, "user1", out.SenderID)
	assert.Equal(t, "YWVz", out.Ciphertext)
	assert.NotEmpty(t, out.ID)

	// sender should receive a self-echo for the sender copy (no ciphertext)
	selfEcho := drainUntilType(t, sender, "message.new", time.Second)
	var echoOut struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		ChannelID string `json:"channel_id"`
		SenderID  string `json:"sender_id"`
	}
	require.NoError(t, json.Unmarshal(selfEcho, &echoOut))
	assert.Equal(t, "message.new", echoOut.Type)
	assert.Equal(t, "ch1", echoOut.ChannelID)
	assert.Equal(t, "user1", echoOut.SenderID)
	assert.Equal(t, "msg-user1", echoOut.ID)
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
