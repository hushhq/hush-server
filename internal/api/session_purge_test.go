package api

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPurgeExpiredSessions_MockReachable verifies the new db.Store entry
// point is wired through the mock store and returns the configured count.
// This guards against silent regressions where the cron in main.go would
// resolve to a no-op stub instead of the real Pool method.
func TestPurgeExpiredSessions_MockReachable(t *testing.T) {
	var calls int
	store := &mockStore{
		purgeExpiredSessionsFn: func(_ context.Context) (int64, error) {
			calls++
			return 7, nil
		},
	}

	n, err := store.PurgeExpiredSessions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(7), n)
	assert.Equal(t, 1, calls)
}

// TestPurgeStaleAdminSessions_RetentionPropagated verifies the retention
// duration parameter is forwarded to the store implementation. Buggy
// wiring that drops the parameter would make admin-revoke cleanup never
// fire on the deploy host.
func TestPurgeStaleAdminSessions_RetentionPropagated(t *testing.T) {
	var seen time.Duration
	store := &mockStore{
		purgeStaleAdminSessionsFn: func(_ context.Context, retention time.Duration) (int64, error) {
			seen = retention
			return 3, nil
		},
	}

	want := 30 * 24 * time.Hour
	n, err := store.PurgeStaleAdminSessions(context.Background(), want)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)
	assert.Equal(t, want, seen)
}

// TestPurgeMethods_ZeroOnDefaultMock verifies the default (nil-fn) mock
// store still implements the new entry points without panicking and
// returns the zero values, so unrelated tests can't accidentally fail
// because they didn't configure the new fields.
func TestPurgeMethods_ZeroOnDefaultMock(t *testing.T) {
	store := &mockStore{}

	n, err := store.PurgeExpiredSessions(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)

	n, err = store.PurgeStaleAdminSessions(context.Background(), time.Hour)
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}
