package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hushhq/hush-server/internal/models"
	"github.com/hushhq/hush-server/internal/transparency"
)

// testNoOpStore is an in-memory TransparencyStore backed by a slice, satisfying
// transparency.TransparencyStore. Used to test TransparencyService in unit tests
// without hitting the DB.
type testNoOpStore struct {
	entries   []models.TransparencyLogEntry
	treeHeads []models.TransparencyTreeHead
}

func (s *testNoOpStore) InsertLogEntry(
	_ context.Context, leafIndex uint64, entry *transparency.LogEntry,
	_, _, _ []byte,
) error {
	s.entries = append(s.entries, models.TransparencyLogEntry{
		ID:         int64(len(s.entries) + 1),
		LeafIndex:  leafIndex,
		Operation:  entry.OperationType,
		UserPubKey: entry.UserPublicKey,
	})
	return nil
}

func (s *testNoOpStore) GetLogEntriesByPubKey(_ context.Context, pubKey []byte) ([]models.TransparencyLogEntry, error) {
	var out []models.TransparencyLogEntry
	for _, e := range s.entries {
		if string(e.UserPubKey) == string(pubKey) {
			out = append(out, e)
		}
	}
	if out == nil {
		out = []models.TransparencyLogEntry{}
	}
	return out, nil
}

func (s *testNoOpStore) GetLatestTreeHead(_ context.Context) (*models.TransparencyTreeHead, error) {
	if len(s.treeHeads) == 0 {
		return nil, nil
	}
	h := s.treeHeads[len(s.treeHeads)-1]
	return &h, nil
}

func (s *testNoOpStore) GetAllLeafHashes(_ context.Context) ([][32]byte, error) {
	var hashes [][32]byte
	for _, e := range s.entries {
		var h [32]byte
		copy(h[:], e.LeafHash)
		hashes = append(hashes, h)
	}
	return hashes, nil
}

func (s *testNoOpStore) InsertTreeHead(_ context.Context, treeSize uint64, rootHash, fringe, headSig []byte) error {
	s.treeHeads = append(s.treeHeads, models.TransparencyTreeHead{
		TreeSize: treeSize,
		RootHash: rootHash,
		Fringe:   fringe,
		HeadSig:  headSig,
	})
	return nil
}

// newTestTransparencySvc creates a TransparencyService backed by the testNoOpStore.
func newTestTransparencySvc(t *testing.T) (*transparency.TransparencyService, *testNoOpStore) {
	t.Helper()
	signer, err := transparency.NewEphemeralLogSigner()
	require.NoError(t, err)
	store := &testNoOpStore{}
	svc, err := transparency.NewTransparencyService(store, signer)
	require.NoError(t, err)
	return svc, store
}

// ---------- GET /api/transparency/verify ----------

func TestTransparencyVerify_MissingPubkey(t *testing.T) {
	mockSt := &mockStore{}
	svc, _ := newTestTransparencySvc(t)
	token := makeAuth(mockSt, "user-1")
	router := TransparencyRoutes(svc, mockSt, testJWTSecret)

	req := httptest.NewRequest(http.MethodGet, "/verify", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Contains(t, body["error"], "pubkey")
}

func TestTransparencyVerify_NoPubkeyEntries(t *testing.T) {
	mockSt := &mockStore{}
	svc, _ := newTestTransparencySvc(t)
	token := makeAuth(mockSt, "user-1")
	router := TransparencyRoutes(svc, mockSt, testJWTSecret)

	// 32 bytes of zero — valid Ed25519 length but no entries in the log.
	unknownKey := hex.EncodeToString(make([]byte, 32))
	req := httptest.NewRequest(http.MethodGet, "/verify?pubkey="+unknownKey, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	entries, ok := body["entries"].([]interface{})
	assert.True(t, ok, "entries must be an array")
	assert.Len(t, entries, 0, "no entries for unknown public key")
}

func TestTransparencyVerify_ValidPubkey(t *testing.T) {
	mockSt := &mockStore{}
	svc, _ := newTestTransparencySvc(t)
	token := makeAuth(mockSt, "user-1")

	// Append an unsigned entry (MVP mode: client-side signing added in T.1 Plan 03).
	pubKey := make([]byte, 32)
	pubKey[0] = 0xAB
	require.NoError(t, svc.AppendEntrySkipSig(context.Background(), &transparency.LogEntry{
		OperationType: transparency.OpRegister,
		UserPublicKey: pubKey,
		Timestamp:     1000,
	}))

	router := TransparencyRoutes(svc, mockSt, testJWTSecret)
	req := httptest.NewRequest(http.MethodGet, "/verify?pubkey="+hex.EncodeToString(pubKey), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))

	entries, ok := body["entries"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, entries, 1, "one entry for registered key")

	proofs, ok := body["proofs"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, proofs, 1, "one proof for the entry")

	treeHead, ok := body["treeHead"].(map[string]interface{})
	assert.True(t, ok, "treeHead must be present")
	assert.Contains(t, treeHead, "size")
	assert.Contains(t, treeHead, "root")
}

func TestTransparencyVerify_InvalidPubkeyHex(t *testing.T) {
	mockSt := &mockStore{}
	svc, _ := newTestTransparencySvc(t)
	token := makeAuth(mockSt, "user-1")
	router := TransparencyRoutes(svc, mockSt, testJWTSecret)

	req := httptest.NewRequest(http.MethodGet, "/verify?pubkey=notvalidhex", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestTransparencyVerify_InvalidPubkeyLength(t *testing.T) {
	mockSt := &mockStore{}
	svc, _ := newTestTransparencySvc(t)
	token := makeAuth(mockSt, "user-1")
	router := TransparencyRoutes(svc, mockSt, testJWTSecret)

	// 16 bytes — not Ed25519 length.
	shortKey := hex.EncodeToString(make([]byte, 16))
	req := httptest.NewRequest(http.MethodGet, "/verify?pubkey="+shortKey, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestTransparencyVerify_RequiresAuth(t *testing.T) {
	mockSt := &mockStore{}
	svc, _ := newTestTransparencySvc(t)
	router := TransparencyRoutes(svc, mockSt, testJWTSecret)

	pubKey := hex.EncodeToString(make([]byte, 32))
	req := httptest.NewRequest(http.MethodGet, "/verify?pubkey="+pubKey, nil)
	// No Authorization header.
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// ---------- Handshake: transparency_url and log_public_key ----------

func TestHandshake_TransparencyURL_OmittedByDefault(t *testing.T) {
	cache := NewInstanceCache()
	handler := HandshakeHandler(cache, false)

	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	_, hasURL := resp["transparency_url"]
	assert.False(t, hasURL, "transparency_url must be omitted when nil")
	_, hasKey := resp["log_public_key"]
	assert.False(t, hasKey, "log_public_key must be omitted when nil")
}

func TestHandshake_TransparencyURL_PresentWhenConfigured(t *testing.T) {
	cache := NewInstanceCache()
	cache.Set("Test", nil, "open", "allowed", 2, "open")
	tURL := "https://transparency.example.com"
	logPubHex := hex.EncodeToString(make([]byte, 32))
	cache.SetTransparencyInfo(&tURL, &logPubHex)

	handler := HandshakeHandler(cache, false)
	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, tURL, resp["transparency_url"])
	assert.Equal(t, logPubHex, resp["log_public_key"])
}
