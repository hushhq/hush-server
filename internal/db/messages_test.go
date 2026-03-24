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
	msg, err := pool.InsertMessage(ctx, channelID, u1ID, nil, ciphertext)
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.NotEmpty(t, msg.ID)
	assert.Equal(t, channelID, msg.ChannelID)
	assert.Equal(t, u1ID, msg.SenderID)
	assert.Equal(t, ciphertext, msg.Ciphertext)
	assert.False(t, msg.Timestamp.IsZero())

	list, err := pool.GetMessages(ctx, channelID, u1ID, time.Time{}, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, msg.ID, list[0].ID)

	_, err = pool.InsertMessage(ctx, channelID, u2ID, nil, []byte("second"))
	require.NoError(t, err)
	list, err = pool.GetMessages(ctx, channelID, u1ID, time.Time{}, 10)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, u2ID, list[0].SenderID)
	assert.Equal(t, u1ID, list[1].SenderID)

	list, err = pool.GetMessages(ctx, channelID, u1ID, list[1].Timestamp, 10)
	require.NoError(t, err)
	require.Len(t, list, 0)
}
