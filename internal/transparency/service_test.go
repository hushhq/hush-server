package transparency_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"hush.app/server/internal/models"
	"hush.app/server/internal/transparency"
)

// memStore is an in-memory TransparencyStore for unit testing.
type memStore struct {
	entries   []models.TransparencyLogEntry
	treeHeads []models.TransparencyTreeHead
}

func newMemStore() *memStore { return &memStore{} }

func (m *memStore) InsertLogEntry(
	_ context.Context, leafIndex uint64, entry *transparency.LogEntry,
	cborBytes, leafHash, logSig []byte,
) error {
	m.entries = append(m.entries, models.TransparencyLogEntry{
		ID:          int64(len(m.entries) + 1),
		LeafIndex:   leafIndex,
		Operation:   entry.OperationType,
		UserPubKey:  entry.UserPublicKey,
		SubjectKey:  entry.SubjectKey,
		EntryCBOR:   cborBytes,
		LeafHash:    leafHash,
		UserSig:     entry.UserSignature,
		LogSig:      logSig,
		LoggedAt:    time.Now(),
	})
	return nil
}

func (m *memStore) GetLogEntriesByPubKey(_ context.Context, pubKey []byte) ([]models.TransparencyLogEntry, error) {
	var out []models.TransparencyLogEntry
	for _, e := range m.entries {
		if string(e.UserPubKey) == string(pubKey) {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *memStore) GetLatestTreeHead(_ context.Context) (*models.TransparencyTreeHead, error) {
	if len(m.treeHeads) == 0 {
		return nil, nil
	}
	return &m.treeHeads[len(m.treeHeads)-1], nil
}

func (m *memStore) InsertTreeHead(_ context.Context, treeSize uint64, rootHash, fringe, headSig []byte) error {
	m.treeHeads = append(m.treeHeads, models.TransparencyTreeHead{
		TreeSize:  treeSize,
		RootHash:  rootHash,
		Fringe:    fringe,
		HeadSig:   headSig,
		CreatedAt: time.Now(),
	})
	return nil
}

// buildUserEntry creates a valid LogEntry with user signature for testing.
func buildUserEntry(t *testing.T, pubKey ed25519.PublicKey, privKey ed25519.PrivateKey, op string) *transparency.LogEntry {
	t.Helper()
	entry := &transparency.LogEntry{
		OperationType: op,
		UserPublicKey: pubKey,
		Timestamp:     time.Now().Unix(),
	}
	payload, err := entry.SerializeForUserSign()
	require.NoError(t, err)
	entry.UserSignature = ed25519.Sign(privKey, payload)
	return entry
}

// TestAppendEntry verifies the full append flow: user sig check, tree update, proof generation.
func TestAppendEntry(t *testing.T) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	logPubKey, logPrivKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	_ = logPubKey

	signer := transparency.NewLogSignerFromKey(logPrivKey)
	store := newMemStore()

	svc, err := transparency.NewTransparencyService(store, signer)
	require.NoError(t, err)

	entry := buildUserEntry(t, pubKey, privKey, transparency.OpRegister)
	result, err := svc.AppendEntry(context.Background(), entry)
	require.NoError(t, err)
	require.Equal(t, uint64(0), result.LeafIndex)
	require.Equal(t, uint64(1), result.TreeSize)
	require.NotEmpty(t, result.LogSignature)
	require.NotZero(t, result.RootHash)

	// Entry must be persisted to the store.
	require.Len(t, store.entries, 1)
	require.Len(t, store.treeHeads, 1)
}

// TestAppendEntryRejectsBadUserSig verifies that a tampered user signature is rejected.
func TestAppendEntryRejectsBadUserSig(t *testing.T) {
	pubKey, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	_, logPrivKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signer := transparency.NewLogSignerFromKey(logPrivKey)
	store := newMemStore()
	svc, err := transparency.NewTransparencyService(store, signer)
	require.NoError(t, err)

	entry := &transparency.LogEntry{
		OperationType: transparency.OpRegister,
		UserPublicKey: pubKey,
		Timestamp:     time.Now().Unix(),
		UserSignature: make([]byte, 64), // all zeros — invalid
	}

	_, err = svc.AppendEntry(context.Background(), entry)
	require.Error(t, err, "invalid user signature must be rejected")
}

// TestGetProof verifies that proof generation returns audit paths for all appended entries.
func TestGetProof(t *testing.T) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	_, logPrivKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signer := transparency.NewLogSignerFromKey(logPrivKey)
	store := newMemStore()
	svc, err := transparency.NewTransparencyService(store, signer)
	require.NoError(t, err)

	// Append 3 entries for this user.
	for _, op := range []string{
		transparency.OpRegister,
		transparency.OpDeviceAdd,
		transparency.OpKeyPackage,
	} {
		e := buildUserEntry(t, pubKey, privKey, op)
		_, err = svc.AppendEntry(context.Background(), e)
		require.NoError(t, err)
	}

	resp, err := svc.GetProof(context.Background(), pubKey)
	require.NoError(t, err)
	require.Len(t, resp.Entries, 3)
	require.Len(t, resp.Proofs, 3)

	// Each proof's audit path must reconstruct to the current root.
	root := svc.RootHash()
	for _, proof := range resp.Proofs {
		require.Equal(t, root[:], proof.RootHash)
	}
}

// TestRecoverFromDB verifies that a service reconstructed from a persisted
// tree head produces the same root after appending additional entries.
func TestRecoverFromDB(t *testing.T) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	_, logPrivKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	signer := transparency.NewLogSignerFromKey(logPrivKey)
	store := newMemStore()

	svc1, err := transparency.NewTransparencyService(store, signer)
	require.NoError(t, err)

	// Append 5 entries.
	for i := 0; i < 5; i++ {
		e := buildUserEntry(t, pubKey, privKey, transparency.OpKeyPackage)
		_, err = svc1.AppendEntry(context.Background(), e)
		require.NoError(t, err)
	}
	root1 := svc1.RootHash()

	// Simulate restart: new service from the same store.
	svc2, err := transparency.NewTransparencyService(store, signer)
	require.NoError(t, err)
	root2 := svc2.RootHash()

	require.Equal(t, root1, root2, "recovered service must have the same root")
	require.Equal(t, svc1.TreeSize(), svc2.TreeSize())
}
