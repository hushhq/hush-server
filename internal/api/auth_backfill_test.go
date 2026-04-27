package api

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ans23 / F5 regression: ensureVerifiedDeviceRegistered must use the
// missing-only backfill path, never overwrite an existing device-keys row.
// These tests pin that behaviour via the store-call surface so a future
// refactor cannot silently revert to the upsert form.

func TestEnsureVerifiedDeviceRegistered_MissingDevice_BackfillsAndUpserts(t *testing.T) {
	var (
		backfillCalled  bool
		backfilledKey   []byte
		upsertCalled    bool
	)
	store := &mockStore{
		backfillRootDeviceKeyFn: func(_ context.Context, userID, deviceID string, key []byte) (bool, error) {
			backfillCalled = true
			backfilledKey = append(backfilledKey[:0], key...)
			assert.Equal(t, "user-1", userID)
			assert.Equal(t, "device-1", deviceID)
			return true, nil
		},
		upsertDeviceFn: func(_ context.Context, userID, deviceID, label string) error {
			upsertCalled = true
			assert.Equal(t, "user-1", userID)
			assert.Equal(t, "device-1", deviceID)
			assert.Equal(t, "", label)
			return nil
		},
		insertDeviceKeyFn: func(context.Context, string, string, string, []byte, []byte) error {
			t.Fatalf("InsertDeviceKey must not be called from /verify backfill")
			return nil
		},
	}
	h := &authHandler{store: store}

	pub := []byte{1, 2, 3, 4, 5}
	h.ensureVerifiedDeviceRegistered(context.Background(), "user-1", "device-1", pub)

	assert.True(t, backfillCalled, "BackfillRootDeviceKey must be called")
	assert.True(t, bytes.Equal(backfilledKey, pub), "the backfill must use the verified public key")
	assert.True(t, upsertCalled, "device row must be created when the backfill inserted")
}

func TestEnsureVerifiedDeviceRegistered_ExistingDevice_DoesNotOverwrite(t *testing.T) {
	var (
		backfillCalled bool
		upsertCalled   bool
	)
	store := &mockStore{
		backfillRootDeviceKeyFn: func(_ context.Context, _, _ string, _ []byte) (bool, error) {
			backfillCalled = true
			return false, nil // row already existed
		},
		upsertDeviceFn: func(context.Context, string, string, string) error {
			upsertCalled = true
			return nil
		},
		insertDeviceKeyFn: func(context.Context, string, string, string, []byte, []byte) error {
			t.Fatalf("InsertDeviceKey must not be called from /verify backfill")
			return nil
		},
	}
	h := &authHandler{store: store}

	h.ensureVerifiedDeviceRegistered(context.Background(), "user-1", "device-1", []byte{9})

	assert.True(t, backfillCalled, "BackfillRootDeviceKey must still be called")
	assert.False(t, upsertCalled, "UpsertDevice must NOT run when the device row already exists")
}

func TestEnsureVerifiedDeviceRegistered_EmptyDeviceID_NoOp(t *testing.T) {
	store := &mockStore{
		backfillRootDeviceKeyFn: func(context.Context, string, string, []byte) (bool, error) {
			t.Fatalf("BackfillRootDeviceKey must not be called when deviceID is empty")
			return false, nil
		},
	}
	h := &authHandler{store: store}

	h.ensureVerifiedDeviceRegistered(context.Background(), "user-1", "  ", []byte{1})
}
