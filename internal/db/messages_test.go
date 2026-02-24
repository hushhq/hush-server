package db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPool_InsertMessage_GetMessages_IsChannelMember_Integration verifies message and membership operations.
func TestPool_InsertMessage_GetMessages_IsChannelMember_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx, `TRUNCATE messages, server_members, channels, servers, sessions, users CASCADE`)
	require.NoError(t, err)

	hash := "hash"
	u1, err := pool.CreateUser(ctx, "user1_"+uuid.New().String()[:8], "User 1", &hash)
	require.NoError(t, err)
	u2, err := pool.CreateUser(ctx, "user2_"+uuid.New().String()[:8], "User 2", &hash)
	require.NoError(t, err)

	var serverID string
	err = pool.QueryRow(ctx, `INSERT INTO servers (name, owner_id) VALUES ('s1', $1) RETURNING id`, u1.ID).Scan(&serverID)
	require.NoError(t, err)

	var channelID string
	err = pool.QueryRow(ctx, `INSERT INTO channels (server_id, name, type) VALUES ($1, 'general', 'text') RETURNING id`, serverID).Scan(&channelID)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO server_members (server_id, user_id, role) VALUES ($1, $2, 'member')`, serverID, u1.ID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO server_members (server_id, user_id, role) VALUES ($1, $2, 'member')`, serverID, u2.ID)
	require.NoError(t, err)

	ok, err := pool.IsChannelMember(ctx, channelID, u1.ID)
	require.NoError(t, err)
	assert.True(t, ok)
	ok, err = pool.IsChannelMember(ctx, channelID, u2.ID)
	require.NoError(t, err)
	assert.True(t, ok)

	unknownChannel := uuid.New().String()
	ok, err = pool.IsChannelMember(ctx, unknownChannel, u1.ID)
	require.NoError(t, err)
	assert.False(t, ok)

	ciphertext := []byte("encrypted-payload")
	msg, err := pool.InsertMessage(ctx, channelID, u1.ID, ciphertext)
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.NotEmpty(t, msg.ID)
	assert.Equal(t, channelID, msg.ChannelID)
	assert.Equal(t, u1.ID, msg.SenderID)
	assert.Equal(t, ciphertext, msg.Ciphertext)
	assert.False(t, msg.Timestamp.IsZero())

	list, err := pool.GetMessages(ctx, channelID, time.Time{}, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, msg.ID, list[0].ID)

	_, err = pool.InsertMessage(ctx, channelID, u2.ID, []byte("second"))
	require.NoError(t, err)
	list, err = pool.GetMessages(ctx, channelID, time.Time{}, 10)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, u2.ID, list[0].SenderID)
	assert.Equal(t, u1.ID, list[1].SenderID)

	list, err = pool.GetMessages(ctx, channelID, list[1].Timestamp, 10)
	require.NoError(t, err)
	require.Len(t, list, 0)
}
