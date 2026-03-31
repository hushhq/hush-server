package transparency_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/hushhq/hush-server/internal/transparency"
)

// TestEntryCBORDeterminism verifies that marshaling the same LogEntry twice
// produces identical bytes (CoreDetEncOptions guarantees determinism).
func TestEntryCBORDeterminism(t *testing.T) {
	pubKey, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	entry := &transparency.LogEntry{
		OperationType: "register",
		UserPublicKey: pubKey,
		SubjectKey:    nil,
		Timestamp:     1711296000,
	}

	b1, err := entry.MarshalCBOR()
	require.NoError(t, err)
	b2, err := entry.MarshalCBOR()
	require.NoError(t, err)

	require.True(t, bytes.Equal(b1, b2), "CBOR encoding must be deterministic")
}

// TestSerializeForUserSign checks that only fields 1-4 are included (no UserSignature).
func TestSerializeForUserSign(t *testing.T) {
	pubKey, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	entry := &transparency.LogEntry{
		OperationType: "device_add",
		UserPublicKey: pubKey,
		SubjectKey:    pubKey, // non-nil subject
		Timestamp:     1711296000,
		UserSignature: []byte("fake-signature"), // field 5 must NOT appear in payload
	}

	payload, err := entry.SerializeForUserSign()
	require.NoError(t, err)
	require.NotEmpty(t, payload)

	// The full CBOR must be longer (includes field 5)
	full, err := entry.MarshalCBOR()
	require.NoError(t, err)
	require.Greater(t, len(full), len(payload), "full CBOR must be larger than sign payload")
}

// TestLogEntryLeafHash verifies LeafHash produces a non-zero 32-byte value.
func TestLogEntryLeafHash(t *testing.T) {
	pubKey, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	entry := &transparency.LogEntry{
		OperationType: "register",
		UserPublicKey: pubKey,
		Timestamp:     1711296000,
	}
	// Add a stub UserSignature so MarshalCBOR includes all fields
	entry.UserSignature = make([]byte, 64)

	h1, err := entry.LeafHash()
	require.NoError(t, err)
	require.Len(t, h1, 32)

	// Two calls must produce the same hash
	h2, err := entry.LeafHash()
	require.NoError(t, err)
	require.Equal(t, h1, h2)
}

// TestDualSignature verifies the user signs fields 1-4, and the log countersigns
// the full entry (fields 1-5). Both signatures verify independently.
func TestDualSignature(t *testing.T) {
	// User keypair — GenerateKey returns (public, private, error)
	userPubKey, userPrivKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	entry := &transparency.LogEntry{
		OperationType: "register",
		UserPublicKey: userPubKey,
		SubjectKey:    nil,
		Timestamp:     1711296000,
	}

	// Step 1: user signs fields 1-4
	payload, err := entry.SerializeForUserSign()
	require.NoError(t, err)
	entry.UserSignature = ed25519.Sign(userPrivKey, payload)

	// User signature must verify
	require.True(t, ed25519.Verify(userPubKey, payload, entry.UserSignature))

	// Step 2: log countersigns full entry
	logPubKey, logPrivKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	signer := transparency.NewLogSignerFromKey(logPrivKey)

	logSig, err := signer.Countersign(entry, 42, [32]byte{})
	require.NoError(t, err)
	require.NotEmpty(t, logSig)

	// The full entry CBOR + leafIndex + root is what the log signed.
	// Verify it using the log's public key.
	fullCBOR, err := entry.MarshalCBOR()
	require.NoError(t, err)
	var root [32]byte
	leafIndexBytes := []byte{0, 0, 0, 0, 0, 0, 0, 42} // big-endian uint64(42)
	signedMsg := append(fullCBOR, leafIndexBytes...)
	signedMsg = append(signedMsg, root[:]...)
	require.True(t, ed25519.Verify(logPubKey, signedMsg, logSig), "log countersignature must verify")
}

// TestLoadLogSignerFromEnv verifies env loading with a valid hex-encoded seed.
func TestLoadLogSignerFromEnv(t *testing.T) {
	// Generate a valid Ed25519 seed (32 bytes)
	seed := make([]byte, 32)
	_, err := rand.Read(seed)
	require.NoError(t, err)

	t.Setenv("TRANSPARENCY_LOG_PRIVATE_KEY", hex.EncodeToString(seed))
	t.Setenv("PRODUCTION", "")

	signer, err := transparency.LoadLogSignerFromEnv()
	require.NoError(t, err)
	require.NotNil(t, signer)
	require.Len(t, signer.PublicKey(), 32)
}

// TestLoadLogSignerFromEnvMissingDevMode verifies that a missing key in dev mode
// returns an ephemeral signer (no error).
func TestLoadLogSignerFromEnvMissingDevMode(t *testing.T) {
	os.Unsetenv("TRANSPARENCY_LOG_PRIVATE_KEY")
	t.Setenv("PRODUCTION", "")

	signer, err := transparency.LoadLogSignerFromEnv()
	require.NoError(t, err)
	require.NotNil(t, signer)
}

// TestLoadLogSignerFromEnvMissingProduction verifies that a missing key in
// production mode returns an error.
func TestLoadLogSignerFromEnvMissingProduction(t *testing.T) {
	os.Unsetenv("TRANSPARENCY_LOG_PRIVATE_KEY")
	t.Setenv("PRODUCTION", "true")

	_, err := transparency.LoadLogSignerFromEnv()
	require.Error(t, err, "production mode with no key must return error")
}
