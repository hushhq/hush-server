package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDeleteChannelTree_SingleChannel_Integration verifies the simple
// (non-category) path against a real Postgres: the row is removed, the
// returned id matches, and the explicit `::uuid[]` cast in the cascade
// SQL accepts a Go []string without tripping `operator does not exist:
// uuid = text` (SQLSTATE 42883). Skips when TEST_DATABASE_URL is unset.
func TestDeleteChannelTree_SingleChannel_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`TRUNCATE attachments, messages, channels, server_members, dm_pairs, servers, sessions, users RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	srv, err := pool.CreateServer(ctx, nil)
	require.NoError(t, err)

	ch, err := pool.CreateChannel(ctx, srv.ID, []byte(`{"n":"general"}`), "text", nil, 0)
	require.NoError(t, err)

	deletedIDs, storageKeys, err := pool.DeleteChannelTree(ctx, ch.ID, srv.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{ch.ID}, deletedIDs)
	assert.Empty(t, storageKeys, "channel had no attachments")

	// Row really gone.
	gone, err := pool.GetChannelByID(ctx, ch.ID)
	require.NoError(t, err)
	assert.Nil(t, gone)
}

// TestDeleteChannelTree_Category_CascadesAndCollectsKeys_Integration is
// the full happy path: a category with two children and one attachment
// each. The single-statement DELETE … RETURNING id must report all
// three ids; the storage_keys snapshot must come back populated even
// though the attachments table cascades on channel_id.
func TestDeleteChannelTree_Category_CascadesAndCollectsKeys_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`TRUNCATE attachments, messages, channels, server_members, dm_pairs, servers, sessions, users RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	owner := newTestUser(t, pool, ctx, "Owner")
	srv, err := pool.CreateServer(ctx, nil)
	require.NoError(t, err)

	cat, err := pool.CreateChannel(ctx, srv.ID, []byte(`{"n":"general"}`), "category", nil, 0)
	require.NoError(t, err)
	childA, err := pool.CreateChannel(ctx, srv.ID, []byte(`{"n":"text-a"}`), "text", &cat.ID, 0)
	require.NoError(t, err)
	childB, err := pool.CreateChannel(ctx, srv.ID, []byte(`{"n":"voice-b"}`), "voice", &cat.ID, 1)
	require.NoError(t, err)

	// Two attachments, one per child. Storage keys are arbitrary
	// opaque strings; the delete handler hands them to the configured
	// blob backend, not the DB.
	_, err = pool.InsertAttachment(ctx, childA.ID, owner, "key/child-a", "image/png", 1024)
	require.NoError(t, err)
	_, err = pool.InsertAttachment(ctx, childB.ID, owner, "key/child-b", "video/mp4", 4096)
	require.NoError(t, err)

	deletedIDs, storageKeys, err := pool.DeleteChannelTree(ctx, cat.ID, srv.ID)
	require.NoError(t, err)
	// Strict ordering, not just set membership: the contract promises
	// children first and the root last so callers (broadcast loops,
	// audit logs) get a deterministic shape. Postgres' RETURNING does
	// not preserve input-array order, so the implementation must
	// reorder explicitly — pin that behaviour here.
	assert.Equal(t, []string{childA.ID, childB.ID, cat.ID}, deletedIDs)
	assert.ElementsMatch(t, []string{"key/child-a", "key/child-b"}, storageKeys)

	// Every row must be gone — no orphaned children, no surviving
	// category. parent_id ON DELETE SET NULL would have left
	// childA/childB with parent_id=NULL if the cascade had skipped
	// them; the explicit DELETE … ANY($::uuid[]) prevents that.
	for _, id := range []string{cat.ID, childA.ID, childB.ID} {
		gone, err := pool.GetChannelByID(ctx, id)
		require.NoError(t, err, "GetChannelByID for %s", id)
		assert.Nil(t, gone, "channel %s must be gone", id)
	}
}

// TestDeleteChannelTree_CrossGuild_ReturnsErrNoRows_Integration: a
// caller targeting another server's category must receive
// pgx.ErrNoRows so the handler maps it to 404 without leaking
// existence. Defence in depth alongside the handler-level guard.
func TestDeleteChannelTree_CrossGuild_ReturnsErrNoRows_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()

	ctx := context.Background()
	_, err := pool.Exec(ctx,
		`TRUNCATE attachments, messages, channels, server_members, dm_pairs, servers, sessions, users RESTART IDENTITY CASCADE`)
	require.NoError(t, err)

	srvA, err := pool.CreateServer(ctx, nil)
	require.NoError(t, err)
	srvB, err := pool.CreateServer(ctx, nil)
	require.NoError(t, err)

	chA, err := pool.CreateChannel(ctx, srvA.ID, []byte(`{"n":"a"}`), "text", nil, 0)
	require.NoError(t, err)

	_, _, err = pool.DeleteChannelTree(ctx, chA.ID, srvB.ID)
	assert.True(t, errors.Is(err, pgx.ErrNoRows), "cross-guild delete must surface ErrNoRows, got: %v", err)
}
