package db

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const truncateReadMarkersTablesSQL = `
TRUNCATE read_markers, messages, channels, server_members, dm_pairs, servers, sessions, users
RESTART IDENTITY CASCADE`

// TestGetUnreadCount_NoMarker_CountsVisible_Integration verifies that when a user
// has no read marker, all visible messages from other senders are counted.
func TestGetUnreadCount_NoMarker_CountsVisible_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx, truncateReadMarkersTablesSQL)
	require.NoError(t, err)

	senderID := newTestUser(t, pool, ctx, "Sender")
	recipientID := newTestUser(t, pool, ctx, "Recipient")

	_, dmCh, err := pool.CreateDMGuild(ctx, senderID, recipientID)
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		_, err = pool.InsertMessage(ctx, dmCh.ID, &senderID, nil, nil, []byte("msg"))
		require.NoError(t, err)
		time.Sleep(2 * time.Millisecond)
	}

	count, err := pool.GetUnreadCount(ctx, dmCh.ID, recipientID)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

// TestGetUnreadCount_WithMarker_CountsOnlyNewer_Integration verifies that after
// marking read at msg[1], only the 3 subsequent messages are counted as unread.
func TestGetUnreadCount_WithMarker_CountsOnlyNewer_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx, truncateReadMarkersTablesSQL)
	require.NoError(t, err)

	senderID := newTestUser(t, pool, ctx, "Sender")
	recipientID := newTestUser(t, pool, ctx, "Recipient")

	_, dmCh, err := pool.CreateDMGuild(ctx, senderID, recipientID)
	require.NoError(t, err)

	msgs := make([]string, 5)
	for i := 0; i < 5; i++ {
		msg, err := pool.InsertMessage(ctx, dmCh.ID, &senderID, nil, nil, []byte("msg"))
		require.NoError(t, err)
		msgs[i] = msg.ID
		time.Sleep(2 * time.Millisecond)
	}

	// Mark read at msg[1] (index 1, the second message).
	err = pool.MarkChannelRead(ctx, dmCh.ID, recipientID, msgs[1])
	require.NoError(t, err)

	// msgs[2], msgs[3], msgs[4] are newer, so count=3.
	count, err := pool.GetUnreadCount(ctx, dmCh.ID, recipientID)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

// TestGetUnreadCount_OwnMessages_NotCounted_Integration verifies that messages
// sent by the querying user are excluded from their own unread count.
func TestGetUnreadCount_OwnMessages_NotCounted_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx, truncateReadMarkersTablesSQL)
	require.NoError(t, err)

	u1ID := newTestUser(t, pool, ctx, "User1")
	u2ID := newTestUser(t, pool, ctx, "User2")

	_, dmCh, err := pool.CreateDMGuild(ctx, u1ID, u2ID)
	require.NoError(t, err)

	// u1 sends 2 messages, u2 sends 1 message.
	_, err = pool.InsertMessage(ctx, dmCh.ID, &u1ID, nil, nil, []byte("from u1 a"))
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	_, err = pool.InsertMessage(ctx, dmCh.ID, &u1ID, nil, nil, []byte("from u1 b"))
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	_, err = pool.InsertMessage(ctx, dmCh.ID, &u2ID, nil, nil, []byte("from u2"))
	require.NoError(t, err)

	// u1's unread count should only be 1 (the message from u2).
	count, err := pool.GetUnreadCount(ctx, dmCh.ID, u1ID)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// TestGetUnreadCount_FanOut_CountsOnlyForRecipient_Integration verifies that a
// fan-out message addressed to u2 is not counted in u3's unread total.
func TestGetUnreadCount_FanOut_CountsOnlyForRecipient_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx, truncateReadMarkersTablesSQL)
	require.NoError(t, err)

	u1ID := newTestUser(t, pool, ctx, "User1")
	u2ID := newTestUser(t, pool, ctx, "User2")
	u3ID := newTestUser(t, pool, ctx, "User3")

	_, dmCh, err := pool.CreateDMGuild(ctx, u1ID, u2ID)
	require.NoError(t, err)

	// Fan-out message addressed exclusively to u2.
	_, err = pool.InsertMessage(ctx, dmCh.ID, &u1ID, nil, &u2ID, []byte("fanout"))
	require.NoError(t, err)

	// u3 is not the recipient, so their unread count must be 0.
	count, err := pool.GetUnreadCount(ctx, dmCh.ID, u3ID)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

// TestMarkChannelRead_Inserts_Integration verifies a new read marker is inserted
// with the stored message timestamp (not wall-clock now()).
func TestMarkChannelRead_Inserts_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx, truncateReadMarkersTablesSQL)
	require.NoError(t, err)

	senderID := newTestUser(t, pool, ctx, "Sender")
	readerID := newTestUser(t, pool, ctx, "Reader")

	_, dmCh, err := pool.CreateDMGuild(ctx, senderID, readerID)
	require.NoError(t, err)

	msg, err := pool.InsertMessage(ctx, dmCh.ID, &senderID, nil, nil, []byte("hello"))
	require.NoError(t, err)

	err = pool.MarkChannelRead(ctx, dmCh.ID, readerID, msg.ID)
	require.NoError(t, err)

	// Confirm the marker was persisted with the message's stored timestamp.
	var markerTS time.Time
	err = pool.QueryRow(ctx, `
		SELECT read_up_to_ts FROM read_markers
		WHERE channel_id = $1::uuid AND user_id = $2`,
		dmCh.ID, readerID,
	).Scan(&markerTS)
	require.NoError(t, err)
	assert.WithinDuration(t, msg.Timestamp, markerTS, time.Millisecond)
}

// TestMarkChannelRead_DoesNotMoveBackward_Integration verifies that marking read
// at an older message after a newer one does not rewind the marker.
func TestMarkChannelRead_DoesNotMoveBackward_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx, truncateReadMarkersTablesSQL)
	require.NoError(t, err)

	senderID := newTestUser(t, pool, ctx, "Sender")
	readerID := newTestUser(t, pool, ctx, "Reader")

	_, dmCh, err := pool.CreateDMGuild(ctx, senderID, readerID)
	require.NoError(t, err)

	m1, err := pool.InsertMessage(ctx, dmCh.ID, &senderID, nil, nil, []byte("first"))
	require.NoError(t, err)
	time.Sleep(2 * time.Millisecond)
	m2, err := pool.InsertMessage(ctx, dmCh.ID, &senderID, nil, nil, []byte("second"))
	require.NoError(t, err)

	// Mark read at m2 (newer).
	err = pool.MarkChannelRead(ctx, dmCh.ID, readerID, m2.ID)
	require.NoError(t, err)

	// Attempt to move marker back to m1 (older); must be a no-op.
	err = pool.MarkChannelRead(ctx, dmCh.ID, readerID, m1.ID)
	require.NoError(t, err)

	var markerTS time.Time
	err = pool.QueryRow(ctx, `
		SELECT read_up_to_ts FROM read_markers
		WHERE channel_id = $1::uuid AND user_id = $2`,
		dmCh.ID, readerID,
	).Scan(&markerTS)
	require.NoError(t, err)
	// Marker must still point at m2's timestamp.
	assert.WithinDuration(t, m2.Timestamp, markerTS, time.Millisecond)
}

// TestMarkChannelRead_RejectsWrongChannel_Integration verifies that a message ID
// from a different channel is rejected.
func TestMarkChannelRead_RejectsWrongChannel_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx, truncateReadMarkersTablesSQL)
	require.NoError(t, err)

	u1ID := newTestUser(t, pool, ctx, "User1")
	u2ID := newTestUser(t, pool, ctx, "User2")
	u3ID := newTestUser(t, pool, ctx, "User3")

	_, ch1, err := pool.CreateDMGuild(ctx, u1ID, u2ID)
	require.NoError(t, err)
	_, ch2, err := pool.CreateDMGuild(ctx, u1ID, u3ID)
	require.NoError(t, err)

	// Insert message in ch2.
	msg, err := pool.InsertMessage(ctx, ch2.ID, &u1ID, nil, nil, []byte("other channel"))
	require.NoError(t, err)

	// Attempt to mark it as read via ch1; must fail.
	err = pool.MarkChannelRead(ctx, ch1.ID, u1ID, msg.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "message not found or not visible")
}

// TestMarkChannelRead_RejectsInvisibleFanout_Integration verifies that a fan-out
// message addressed to u3 cannot be used to advance u1's read marker.
func TestMarkChannelRead_RejectsInvisibleFanout_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx, truncateReadMarkersTablesSQL)
	require.NoError(t, err)

	u1ID := newTestUser(t, pool, ctx, "User1")
	u2ID := newTestUser(t, pool, ctx, "User2")
	u3ID := newTestUser(t, pool, ctx, "User3")

	_, dmCh, err := pool.CreateDMGuild(ctx, u1ID, u2ID)
	require.NoError(t, err)

	// Fan-out message to u3 only.
	msg, err := pool.InsertMessage(ctx, dmCh.ID, &u2ID, nil, &u3ID, []byte("for u3 only"))
	require.NoError(t, err)

	// u1 tries to mark this message as read; must fail.
	err = pool.MarkChannelRead(ctx, dmCh.ID, u1ID, msg.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "message not found or not visible")
}
