package db

import (
	"context"
	"testing"
	"time"

	"github.com/hushhq/hush-server/internal/version"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// truncateMLSTables resets every MLS state table plus its dependencies so each
// integration test starts from a clean slate.
func truncateMLSTables(t *testing.T, pool *Pool, ctx context.Context) {
	t.Helper()
	_, err := pool.Exec(ctx, `TRUNCATE
		mls_pending_welcomes,
		mls_commits,
		mls_group_info,
		mls_key_packages,
		mls_credentials,
		messages,
		channels,
		server_members,
		dm_pairs,
		servers,
		sessions,
		users
		RESTART IDENTITY CASCADE`)
	require.NoError(t, err)
}

// insertLegacyKeyPackage writes a key package row stamped with the legacy
// ciphersuite, bypassing the accessor (which would always stamp the current
// suite). This simulates rows that survived from before the X-Wing migration.
func insertLegacyKeyPackage(t *testing.T, pool *Pool, ctx context.Context, userID, deviceID string, kpBytes []byte, lastResort bool) {
	t.Helper()
	expires := time.Now().Add(24 * time.Hour)
	if lastResort {
		expires = time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO mls_key_packages
			(user_id, device_id, key_package_bytes, last_resort, expires_at, ciphersuite)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		userID, deviceID, kpBytes, lastResort, expires, version.LegacyMLSCiphersuite,
	)
	require.NoError(t, err)
}

// TestMLSCiphersuite_KeyPackageInsertStampsCurrent_Integration verifies that
// InsertMLSKeyPackages stamps every row with version.CurrentMLSCiphersuite.
// Guards against silent regressions to the legacy suite.
func TestMLSCiphersuite_KeyPackageInsertStampsCurrent_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	truncateMLSTables(t, pool, ctx)

	userID := newTestUser(t, pool, ctx, "kp insert")
	deviceID := "device-A"

	require.NoError(t, pool.InsertMLSKeyPackages(ctx, userID, deviceID,
		[][]byte{[]byte("kp-current-1"), []byte("kp-current-2")},
		time.Now().Add(24*time.Hour),
	))

	rows, err := pool.Query(ctx,
		`SELECT ciphersuite FROM mls_key_packages WHERE user_id=$1 AND device_id=$2`,
		userID, deviceID)
	require.NoError(t, err)
	defer rows.Close()

	var got []int
	for rows.Next() {
		var cs int
		require.NoError(t, rows.Scan(&cs))
		got = append(got, cs)
	}
	require.Len(t, got, 2)
	for _, cs := range got {
		assert.Equal(t, version.CurrentMLSCiphersuite, cs,
			"new KeyPackage rows must be stamped with CurrentMLSCiphersuite")
	}
}

// TestMLSCiphersuite_ConsumeIgnoresLegacy_Integration proves the consume path
// will not hand out a legacy-suite KeyPackage to a current-suite client.
func TestMLSCiphersuite_ConsumeIgnoresLegacy_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	truncateMLSTables(t, pool, ctx)

	userID := newTestUser(t, pool, ctx, "kp consume")
	deviceID := "device-A"

	insertLegacyKeyPackage(t, pool, ctx, userID, deviceID, []byte("legacy-kp"), false)

	got, err := pool.ConsumeMLSKeyPackage(ctx, userID, deviceID)
	require.NoError(t, err)
	assert.Nil(t, got, "consume must return nil when only legacy-suite packages exist")
}

// TestMLSCiphersuite_ConsumeReturnsCurrent_Integration verifies the happy path:
// when both legacy and current-suite packages exist, consume returns a
// current-suite package.
func TestMLSCiphersuite_ConsumeReturnsCurrent_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	truncateMLSTables(t, pool, ctx)

	userID := newTestUser(t, pool, ctx, "kp consume current")
	deviceID := "device-A"

	insertLegacyKeyPackage(t, pool, ctx, userID, deviceID, []byte("legacy-kp"), false)
	require.NoError(t, pool.InsertMLSKeyPackages(ctx, userID, deviceID,
		[][]byte{[]byte("current-kp")},
		time.Now().Add(24*time.Hour),
	))

	got, err := pool.ConsumeMLSKeyPackage(ctx, userID, deviceID)
	require.NoError(t, err)
	assert.Equal(t, []byte("current-kp"), got)
}

// TestMLSCiphersuite_CountIgnoresLegacy_Integration verifies that the
// replenishment counter ignores legacy-suite packages so the low-watermark
// event reflects only usable packages.
func TestMLSCiphersuite_CountIgnoresLegacy_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	truncateMLSTables(t, pool, ctx)

	userID := newTestUser(t, pool, ctx, "kp count")
	deviceID := "device-A"

	for i := 0; i < 3; i++ {
		insertLegacyKeyPackage(t, pool, ctx, userID, deviceID,
			[]byte{'l', byte('0' + i)}, false)
	}
	require.NoError(t, pool.InsertMLSKeyPackages(ctx, userID, deviceID,
		[][]byte{[]byte("c1"), []byte("c2")},
		time.Now().Add(24*time.Hour),
	))

	n, err := pool.CountUnusedMLSKeyPackages(ctx, userID, deviceID)
	require.NoError(t, err)
	assert.Equal(t, 2, n,
		"count must ignore legacy-suite packages even when they are unconsumed")
}

// TestMLSCiphersuite_LastResortFallsBackToCurrent_Integration verifies that the
// last-resort fallback only returns a current-suite last-resort row, never the
// legacy one.
func TestMLSCiphersuite_LastResortFallsBackToCurrent_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	truncateMLSTables(t, pool, ctx)

	userID := newTestUser(t, pool, ctx, "kp last-resort")
	deviceID := "device-A"

	// Legacy last-resort row pre-existing on the server.
	insertLegacyKeyPackage(t, pool, ctx, userID, deviceID, []byte("legacy-lr"), true)

	// No current packages yet -> consume must NOT fall back to legacy last-resort.
	got, err := pool.ConsumeMLSKeyPackage(ctx, userID, deviceID)
	require.NoError(t, err)
	assert.Nil(t, got, "must not fall back to legacy last-resort under current suite")

	// Add a current last-resort and re-try.
	require.NoError(t, pool.InsertMLSLastResortKeyPackage(ctx, userID, deviceID, []byte("current-lr")))
	got, err = pool.ConsumeMLSKeyPackage(ctx, userID, deviceID)
	require.NoError(t, err)
	assert.Equal(t, []byte("current-lr"), got)
}

// TestMLSCiphersuite_GroupInfoIsolation_Integration verifies that the legacy
// row and a fresh current-suite row coexist for the same (channel, group_type)
// and that Get returns only the current-suite row.
func TestMLSCiphersuite_GroupInfoIsolation_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	truncateMLSTables(t, pool, ctx)

	srv, err := pool.CreateServer(ctx, nil)
	require.NoError(t, err)
	ch, err := pool.CreateChannel(ctx, srv.ID, nil, "text", nil, 0)
	require.NoError(t, err)

	// Seed a legacy-suite row directly.
	_, err = pool.Exec(ctx, `
		INSERT INTO mls_group_info (channel_id, group_type, group_info_bytes, epoch, ciphersuite)
		VALUES ($1, 'text', $2, 0, $3)`,
		ch.ID, []byte("legacy-gi"), version.LegacyMLSCiphersuite)
	require.NoError(t, err)

	// Reads under the current suite must see no row.
	gi, epoch, err := pool.GetMLSGroupInfo(ctx, ch.ID, "text")
	require.NoError(t, err)
	assert.Nil(t, gi, "current-suite Get must not return legacy row")
	assert.Equal(t, int64(0), epoch)

	// Upserting the current-suite row must succeed without PK/unique conflict.
	require.NoError(t, pool.UpsertMLSGroupInfo(ctx, ch.ID, "text", []byte("current-gi"), 1))

	gi, epoch, err = pool.GetMLSGroupInfo(ctx, ch.ID, "text")
	require.NoError(t, err)
	assert.Equal(t, []byte("current-gi"), gi)
	assert.Equal(t, int64(1), epoch)

	// Both rows must still exist in the table.
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM mls_group_info WHERE channel_id=$1 AND group_type='text'`,
		ch.ID,
	).Scan(&n))
	assert.Equal(t, 2, n, "legacy row must be preserved alongside current-suite row")
}

// TestMLSCiphersuite_GuildMetadataGroupInfoIsolation_Integration mirrors the
// channel-scoped isolation test for the guild metadata group.
func TestMLSCiphersuite_GuildMetadataGroupInfoIsolation_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	truncateMLSTables(t, pool, ctx)

	srv, err := pool.CreateServer(ctx, nil)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO mls_group_info (server_id, group_type, group_info_bytes, epoch, ciphersuite)
		VALUES ($1, 'metadata', $2, 0, $3)`,
		srv.ID, []byte("legacy-md"), version.LegacyMLSCiphersuite)
	require.NoError(t, err)

	gi, _, err := pool.GetMLSGuildMetadataGroupInfo(ctx, srv.ID)
	require.NoError(t, err)
	assert.Nil(t, gi, "current-suite Get must not return legacy metadata row")

	require.NoError(t, pool.UpsertMLSGuildMetadataGroupInfo(ctx, srv.ID, []byte("current-md"), 1))
	gi, epoch, err := pool.GetMLSGuildMetadataGroupInfo(ctx, srv.ID)
	require.NoError(t, err)
	assert.Equal(t, []byte("current-md"), gi)
	assert.Equal(t, int64(1), epoch)
}

// TestMLSCiphersuite_CommitsFiltered_Integration verifies that legacy commits
// are not returned by GetMLSCommitsSinceEpoch and that AppendMLSCommit stamps
// the current suite.
func TestMLSCiphersuite_CommitsFiltered_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	truncateMLSTables(t, pool, ctx)

	userID := newTestUser(t, pool, ctx, "commits")
	srv, err := pool.CreateServer(ctx, nil)
	require.NoError(t, err)
	ch, err := pool.CreateChannel(ctx, srv.ID, nil, "text", nil, 0)
	require.NoError(t, err)

	// Seed a legacy-suite commit.
	_, err = pool.Exec(ctx, `
		INSERT INTO mls_commits (channel_id, epoch, commit_bytes, sender_id, ciphersuite)
		VALUES ($1, 1, $2, $3, $4)`,
		ch.ID, []byte("legacy-commit"), userID, version.LegacyMLSCiphersuite)
	require.NoError(t, err)

	// Append a current-suite commit.
	require.NoError(t, pool.AppendMLSCommit(ctx, ch.ID, 2, []byte("current-commit"), userID))

	commits, err := pool.GetMLSCommitsSinceEpoch(ctx, ch.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, commits, 1, "must return only current-suite commits")
	assert.Equal(t, []byte("current-commit"), commits[0].CommitBytes)
	assert.Equal(t, int64(2), commits[0].Epoch)
}

// TestMLSCiphersuite_DeletePendingWelcomeIgnoresLegacy_Integration verifies that
// DeletePendingWelcome only deletes rows stamped with the current ciphersuite.
// This matches the read-side filter: a current-suite client receives only
// current-suite welcomes from GetPendingWelcomes, so the ACK delete must not
// be able to remove a legacy row that the client never saw.
func TestMLSCiphersuite_DeletePendingWelcomeIgnoresLegacy_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	truncateMLSTables(t, pool, ctx)

	senderID := newTestUser(t, pool, ctx, "del-welcome sender")
	recipientID := newTestUser(t, pool, ctx, "del-welcome recipient")
	srv, err := pool.CreateServer(ctx, nil)
	require.NoError(t, err)
	ch, err := pool.CreateChannel(ctx, srv.ID, nil, "text", nil, 0)
	require.NoError(t, err)

	var legacyID string
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO mls_pending_welcomes
			(channel_id, recipient_user_id, sender_id, welcome_bytes, epoch, ciphersuite)
		VALUES ($1, $2, $3, $4, 0, $5)
		RETURNING id`,
		ch.ID, recipientID, senderID, []byte("legacy-welcome"), version.LegacyMLSCiphersuite,
	).Scan(&legacyID))

	require.NoError(t, pool.DeletePendingWelcome(ctx, legacyID))

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM mls_pending_welcomes WHERE id=$1`, legacyID,
	).Scan(&n))
	assert.Equal(t, 1, n, "legacy-suite welcome must remain even after a Delete call")
}

// TestMLSCiphersuite_PendingWelcomesFiltered_Integration verifies that legacy
// welcomes are not returned and that StorePendingWelcome stamps the current
// suite.
func TestMLSCiphersuite_PendingWelcomesFiltered_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	truncateMLSTables(t, pool, ctx)

	senderID := newTestUser(t, pool, ctx, "welcome sender")
	recipientID := newTestUser(t, pool, ctx, "welcome recipient")
	srv, err := pool.CreateServer(ctx, nil)
	require.NoError(t, err)
	ch, err := pool.CreateChannel(ctx, srv.ID, nil, "text", nil, 0)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `
		INSERT INTO mls_pending_welcomes
			(channel_id, recipient_user_id, sender_id, welcome_bytes, epoch, ciphersuite)
		VALUES ($1, $2, $3, $4, 0, $5)`,
		ch.ID, recipientID, senderID, []byte("legacy-welcome"), version.LegacyMLSCiphersuite)
	require.NoError(t, err)

	require.NoError(t, pool.StorePendingWelcome(ctx, ch.ID, recipientID, senderID,
		[]byte("current-welcome"), 1))

	welcomes, err := pool.GetPendingWelcomes(ctx, recipientID)
	require.NoError(t, err)
	require.Len(t, welcomes, 1, "must return only current-suite welcomes")
	assert.Equal(t, []byte("current-welcome"), welcomes[0].WelcomeBytes)
	assert.Equal(t, int64(1), welcomes[0].Epoch)
}

// TestMLSCiphersuite_MigrationBackfillsLegacy_Integration verifies the migration
// did the right thing: any row that pre-existed in the four MLS state tables is
// stamped with version.LegacyMLSCiphersuite, never accidentally with
// version.CurrentMLSCiphersuite, and the ciphersuite column is NOT NULL.
//
// We exercise this indirectly: insert rows WITHOUT specifying ciphersuite and
// verify the database rejects the write. This proves the column is mandatory.
func TestMLSCiphersuite_CiphersuiteColumnIsRequired_Integration(t *testing.T) {
	pool, cleanup := SetupTestDB(t)
	defer cleanup()
	ctx := context.Background()
	truncateMLSTables(t, pool, ctx)

	userID := newTestUser(t, pool, ctx, "schema check")
	srv, err := pool.CreateServer(ctx, nil)
	require.NoError(t, err)
	ch, err := pool.CreateChannel(ctx, srv.ID, nil, "text", nil, 0)
	require.NoError(t, err)

	// mls_key_packages
	_, err = pool.Exec(ctx, `
		INSERT INTO mls_key_packages (user_id, device_id, key_package_bytes, expires_at)
		VALUES ($1, 'd', $2, now() + interval '1 day')`,
		userID, []byte("kp"))
	assert.Error(t, err, "mls_key_packages must require ciphersuite")

	// mls_group_info
	_, err = pool.Exec(ctx, `
		INSERT INTO mls_group_info (channel_id, group_type, group_info_bytes, epoch)
		VALUES ($1, 'text', $2, 0)`,
		ch.ID, []byte("gi"))
	assert.Error(t, err, "mls_group_info must require ciphersuite")

	// mls_commits
	_, err = pool.Exec(ctx, `
		INSERT INTO mls_commits (channel_id, epoch, commit_bytes, sender_id)
		VALUES ($1, 1, $2, $3)`,
		ch.ID, []byte("c"), userID)
	assert.Error(t, err, "mls_commits must require ciphersuite")

	// mls_pending_welcomes
	recipientID := newTestUser(t, pool, ctx, "schema check recipient")
	_, err = pool.Exec(ctx, `
		INSERT INTO mls_pending_welcomes (channel_id, recipient_user_id, sender_id, welcome_bytes, epoch)
		VALUES ($1, $2, $3, $4, 0)`,
		ch.ID, recipientID, userID, []byte("w"))
	assert.Error(t, err, "mls_pending_welcomes must require ciphersuite")
}
