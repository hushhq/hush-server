package db

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestUser is a helper that creates a user with a random Ed25519 key for use
// in DB integration tests.
func newTestUser(t *testing.T, pool *Pool, ctx context.Context, displayName string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	username := "testuser_" + uuid.New().String()[:8]
	u, err := pool.CreateUserWithPublicKey(ctx, username, displayName, pub)
	require.NoError(t, err)
	return u.ID
}

// TestPool_InsertMessage_GetMessages_IsChannelMember_Integration verifies message
// and membership operations.
func TestPool_InsertMessage_GetMessages_IsChannelMember_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx, `TRUNCATE messages, channels, sessions, users CASCADE`)
	require.NoError(t, err)

	u1ID := newTestUser(t, pool, ctx, "User 1")
	u2ID := newTestUser(t, pool, ctx, "User 2")

	var channelID string
	err = pool.QueryRow(ctx, `INSERT INTO channels (type) VALUES ('text') RETURNING id`).Scan(&channelID)
	require.NoError(t, err)

	// In single-tenant model, any existing user is a channel member.
	ok, err := pool.IsChannelMember(ctx, channelID, u1ID)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = pool.IsChannelMember(ctx, channelID, u2ID)
	require.NoError(t, err)
	assert.True(t, ok)

	// A non-existent user ID is not a member.
	ok, err = pool.IsChannelMember(ctx, channelID, uuid.New().String())
	require.NoError(t, err)
	assert.False(t, ok)

	ciphertext := []byte("encrypted-payload")
	msg, err := pool.InsertMessage(ctx, channelID, &u1ID, nil, nil, ciphertext)
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.NotEmpty(t, msg.ID)
	assert.Equal(t, channelID, msg.ChannelID)
	require.NotNil(t, msg.SenderID)
	assert.Equal(t, u1ID, *msg.SenderID)
	assert.Equal(t, ciphertext, msg.Ciphertext)
	assert.False(t, msg.Timestamp.IsZero())

	list, err := pool.GetMessages(ctx, channelID, u1ID, time.Time{}, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, msg.ID, list[0].ID)

	_, err = pool.InsertMessage(ctx, channelID, &u2ID, nil, nil, []byte("second"))
	require.NoError(t, err)
	list, err = pool.GetMessages(ctx, channelID, u1ID, time.Time{}, 10)
	require.NoError(t, err)
	require.Len(t, list, 2)
	require.NotNil(t, list[0].SenderID)
	assert.Equal(t, u2ID, *list[0].SenderID)
	require.NotNil(t, list[1].SenderID)
	assert.Equal(t, u1ID, *list[1].SenderID)

	list, err = pool.GetMessages(ctx, channelID, u1ID, list[1].Timestamp, 10)
	require.NoError(t, err)
	require.Len(t, list, 0)
}

// TestPool_GetMessagesAfter_Integration verifies the forward (after-cursor) message query.
func TestPool_GetMessagesAfter_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx, `TRUNCATE messages, channels, sessions, users CASCADE`)
	require.NoError(t, err)

	u1ID := newTestUser(t, pool, ctx, "User 1")
	u2ID := newTestUser(t, pool, ctx, "User 2")

	var channelID string
	err = pool.QueryRow(ctx, `INSERT INTO channels (type) VALUES ('text') RETURNING id`).Scan(&channelID)
	require.NoError(t, err)

	msg1, err := pool.InsertMessage(ctx, channelID, &u1ID, nil, nil, []byte("first"))
	require.NoError(t, err)
	msg2, err := pool.InsertMessage(ctx, channelID, &u2ID, nil, nil, []byte("second"))
	require.NoError(t, err)
	msg3, err := pool.InsertMessage(ctx, channelID, &u1ID, nil, nil, []byte("third"))
	require.NoError(t, err)

	ts1 := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	ts2 := ts1.Add(time.Minute)
	ts3 := ts2.Add(time.Minute)
	_, err = pool.Exec(ctx, `UPDATE messages SET "timestamp" = $1 WHERE id = $2`, ts1, msg1.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE messages SET "timestamp" = $1 WHERE id = $2`, ts2, msg2.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE messages SET "timestamp" = $1 WHERE id = $2`, ts3, msg3.ID)
	require.NoError(t, err)

	// All messages after msg1 timestamp should include msg2 and msg3, in ASC order.
	list, err := pool.GetMessagesAfter(ctx, channelID, u1ID, ts1, 50)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, msg2.ID, list[0].ID)
	assert.Equal(t, msg3.ID, list[1].ID)

	// No messages after msg3 timestamp.
	list, err = pool.GetMessagesAfter(ctx, channelID, u1ID, ts3, 50)
	require.NoError(t, err)
	assert.Len(t, list, 0)

	// Fan-out row for u3 should be invisible to u1.
	u3ID := newTestUser(t, pool, ctx, "User 3")
	msg4, err := pool.InsertMessage(ctx, channelID, &u2ID, nil, &u3ID, []byte("fanout-for-u3"))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE messages SET "timestamp" = $1 WHERE id = $2`, ts3.Add(time.Minute), msg4.ID)
	require.NoError(t, err)
	list, err = pool.GetMessagesAfter(ctx, channelID, u1ID, ts1, 50)
	require.NoError(t, err)
	require.Len(t, list, 2, "fan-out row for u3 must not appear for u1")
	assert.Equal(t, msg2.ID, list[0].ID)
	assert.Equal(t, msg3.ID, list[1].ID)

	// Limit honored.
	list, err = pool.GetMessagesAfter(ctx, channelID, u1ID, ts1, 1)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, msg2.ID, list[0].ID)

	list, err = pool.GetMessagesAfter(ctx, channelID, u1ID, ts1, 0)
	require.NoError(t, err)
	assert.Len(t, list, 0)
}
