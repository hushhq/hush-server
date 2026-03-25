package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hush.app/server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testJWTSecret = "test-secret"
)

var testJWTExpiry = 1 * time.Hour

// ---------- helpers ----------

func newTestRouter(store *mockStore) http.Handler {
	return AuthRoutes(store, testJWTSecret, testJWTExpiry, nil)
}

func postJSON(handler http.Handler, path string, body interface{}) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func getWithAuth(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func postWithAuth(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func decodeAuthResponse(t *testing.T, rr *httptest.ResponseRecorder) models.AuthResponse {
	t.Helper()
	var resp models.AuthResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	return resp
}

func decodeErrorResponse(t *testing.T, rr *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	return resp
}

// newTestUser returns a minimal User for use in mock return values.
func newTestUser(username string) *models.User {
	return &models.User{
		ID:          uuid.New().String(),
		Username:    username,
		DisplayName: username,
		Role:        "member",
		CreatedAt:   time.Now(),
	}
}

// generateEd25519KeyPair generates an Ed25519 keypair and returns the base64-
// encoded public key and the raw private key for signing.
func generateEd25519KeyPair(t *testing.T) (pubBase64 string, priv ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(pub), priv
}

// signNonce signs a nonce hex string with the given private key and returns
// the base64-encoded signature.
func signNonce(nonce string, priv ed25519.PrivateKey) string {
	nonceBytes, err := hex.DecodeString(nonce)
	if err != nil {
		panic("signNonce: invalid hex nonce: " + err.Error())
	}
	sig := ed25519.Sign(priv, nonceBytes)
	return base64.StdEncoding.EncodeToString(sig)
}

// ---------- ValidateUsername (unit) ----------

func TestValidateUsername_ValidInput_ReturnsNil(t *testing.T) {
	validNames := []string{
		"alice", "Alice123", "user.name", "user_name", "user-name", "a", "A1._-b",
	}
	for _, name := range validNames {
		assert.NoError(t, validateUsername(name), "expected nil for %q", name)
	}
}

func TestValidateUsername_Empty_ReturnsError(t *testing.T) {
	err := validateUsername("")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "username is required")
}

func TestValidateUsername_TooLong_ReturnsError(t *testing.T) {
	long := strings.Repeat("a", 129)
	err := validateUsername(long)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "username too long")
}

func TestValidateUsername_InvalidChars_ReturnsError(t *testing.T) {
	invalidNames := []string{"has space", "has@at", "has!bang", "has#hash", "has$dollar"}
	for _, name := range invalidNames {
		err := validateUsername(name)
		assert.Error(t, err, "expected error for %q", name)
		assert.Contains(t, err.Error(), "username may only contain")
	}
}

// ---------- Register ----------

func TestRegister_Success(t *testing.T) {
	pubBase64, _ := generateEd25519KeyPair(t)
	user := newTestUser("alice")

	store := &mockStore{
		createUserWithPublicKeyFn: func(_ context.Context, username, displayName string, _ []byte) (*models.User, error) {
			return user, nil
		},
	}
	router := newTestRouter(store)

	rr := postJSON(router, "/register", models.RegisterRequest{
		Username:  "alice",
		PublicKey: pubBase64,
	})

	assert.Equal(t, http.StatusOK, rr.Code)
	resp := decodeAuthResponse(t, rr)
	assert.NotEmpty(t, resp.Token)
	assert.Equal(t, "alice", resp.User.Username)
}

func TestRegister_EmptyUsername_Returns400(t *testing.T) {
	pubBase64, _ := generateEd25519KeyPair(t)
	store := &mockStore{}
	router := newTestRouter(store)

	rr := postJSON(router, "/register", models.RegisterRequest{
		Username:  "",
		PublicKey: pubBase64,
	})

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Contains(t, resp["error"], "username is required")
}

func TestRegister_InvalidPublicKeyLength_Returns400(t *testing.T) {
	store := &mockStore{}
	router := newTestRouter(store)

	// 16 bytes instead of 32.
	shortKey := base64.StdEncoding.EncodeToString(make([]byte, 16))
	rr := postJSON(router, "/register", models.RegisterRequest{
		Username:  "alice",
		PublicKey: shortKey,
	})

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Contains(t, resp["error"], "32 bytes")
}

func TestRegister_InvalidPublicKeyEncoding_Returns400(t *testing.T) {
	store := &mockStore{}
	router := newTestRouter(store)

	rr := postJSON(router, "/register", models.RegisterRequest{
		Username:  "alice",
		PublicKey: "not-valid-base64!!!",
	})

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestRegister_DuplicatePublicKey_Returns409(t *testing.T) {
	pubBase64, _ := generateEd25519KeyPair(t)
	store := &mockStore{
		createUserWithPublicKeyFn: func(_ context.Context, _, _ string, _ []byte) (*models.User, error) {
			return nil, errors.New("duplicate key value violates unique constraint on root_public_key")
		},
	}
	router := newTestRouter(store)

	rr := postJSON(router, "/register", models.RegisterRequest{
		Username:  "alice",
		PublicKey: pubBase64,
	})

	assert.Equal(t, http.StatusConflict, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Contains(t, resp["error"], "Public key already registered")
}

func TestRegister_DuplicateUsername_Returns409(t *testing.T) {
	pubBase64, _ := generateEd25519KeyPair(t)
	store := &mockStore{
		createUserWithPublicKeyFn: func(_ context.Context, _, _ string, _ []byte) (*models.User, error) {
			return nil, errors.New("duplicate key value violates unique constraint")
		},
	}
	router := newTestRouter(store)

	rr := postJSON(router, "/register", models.RegisterRequest{
		Username:  "alice",
		PublicKey: pubBase64,
	})

	assert.Equal(t, http.StatusConflict, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Contains(t, resp["error"], "Username already taken")
}

func TestRegister_ClosedMode_Returns403(t *testing.T) {
	pubBase64, _ := generateEd25519KeyPair(t)
	store := &mockStore{
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{
				ID:               "inst-1",
				RegistrationMode: "closed",
			}, nil
		},
	}
	router := newTestRouter(store)

	rr := postJSON(router, "/register", models.RegisterRequest{
		Username:  "alice",
		PublicKey: pubBase64,
	})

	assert.Equal(t, http.StatusForbidden, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Contains(t, resp["error"], "closed")
}

// ---------- Challenge ----------

func TestChallenge_ReturnsNonce(t *testing.T) {
	pubBase64, _ := generateEd25519KeyPair(t)
	store := &mockStore{}
	router := newTestRouter(store)

	rr := postJSON(router, "/challenge", models.ChallengeRequest{
		PublicKey: pubBase64,
	})

	assert.Equal(t, http.StatusOK, rr.Code)
	var body map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.NotEmpty(t, body["nonce"])
	// Nonce must be a 64-char hex string (32 bytes).
	assert.Len(t, body["nonce"], 64, "nonce should be 64 hex chars (32 bytes)")
}

func TestChallenge_InvalidPublicKey_Returns400(t *testing.T) {
	store := &mockStore{}
	router := newTestRouter(store)

	rr := postJSON(router, "/challenge", models.ChallengeRequest{
		PublicKey: "not-base64!!!",
	})

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------- Verify ----------

// inMemoryNonceStore wires up InsertAuthNonce + ConsumeAuthNonce with an in-memory map
// so verify tests can exercise the full challenge -> sign -> verify flow.
type inMemoryNonceStore struct {
	nonces map[string][]byte // nonce -> publicKey
}

func newInMemoryNonceStore() *inMemoryNonceStore {
	return &inMemoryNonceStore{nonces: make(map[string][]byte)}
}

func (s *inMemoryNonceStore) insertFn(_ context.Context, nonce string, pubKey []byte, _ time.Time) error {
	s.nonces[nonce] = pubKey
	return nil
}

func (s *inMemoryNonceStore) consumeFn(_ context.Context, nonce string) ([]byte, error) {
	pk, ok := s.nonces[nonce]
	if !ok {
		return nil, errors.New("sql: no rows in result set")
	}
	delete(s.nonces, nonce)
	return pk, nil
}

func TestVerify_ValidSignature_Returns200(t *testing.T) {
	pubBase64, priv := generateEd25519KeyPair(t)
	user := newTestUser("alice")
	nonceStore := newInMemoryNonceStore()

	store := &mockStore{
		insertAuthNonceFn:  nonceStore.insertFn,
		consumeAuthNonceFn: nonceStore.consumeFn,
		getUserByPublicKeyFn: func(_ context.Context, _ []byte) (*models.User, error) {
			return user, nil
		},
	}
	router := newTestRouter(store)

	// Step 1: get nonce.
	rr := postJSON(router, "/challenge", models.ChallengeRequest{PublicKey: pubBase64})
	require.Equal(t, http.StatusOK, rr.Code)
	var challengeResp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&challengeResp))
	nonce := challengeResp["nonce"]

	// Step 2: sign and verify.
	sig := signNonce(nonce, priv)
	rr = postJSON(router, "/verify", models.VerifyRequest{
		PublicKey: pubBase64,
		Nonce:     nonce,
		Signature: sig,
	})

	assert.Equal(t, http.StatusOK, rr.Code)
	resp := decodeAuthResponse(t, rr)
	assert.NotEmpty(t, resp.Token)
	assert.Equal(t, "alice", resp.User.Username)
}

func TestVerify_ExpiredNonce_Returns401(t *testing.T) {
	pubBase64, _ := generateEd25519KeyPair(t)

	store := &mockStore{
		consumeAuthNonceFn: func(_ context.Context, _ string) ([]byte, error) {
			return nil, errors.New("sql: no rows in result set")
		},
	}
	router := newTestRouter(store)

	rr := postJSON(router, "/verify", models.VerifyRequest{
		PublicKey: pubBase64,
		Nonce:     "aabbccdd",
		Signature: base64.StdEncoding.EncodeToString(make([]byte, 64)),
	})

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Contains(t, resp["error"], "Challenge expired")
}

func TestVerify_BadSignature_Returns401(t *testing.T) {
	pubBase64, _ := generateEd25519KeyPair(t)
	_, otherPriv := generateEd25519KeyPair(t)

	nonceStore := newInMemoryNonceStore()
	store := &mockStore{
		insertAuthNonceFn:  nonceStore.insertFn,
		consumeAuthNonceFn: nonceStore.consumeFn,
		getUserByPublicKeyFn: func(_ context.Context, _ []byte) (*models.User, error) {
			return newTestUser("alice"), nil
		},
	}
	router := newTestRouter(store)

	// Get a valid nonce.
	rr := postJSON(router, "/challenge", models.ChallengeRequest{PublicKey: pubBase64})
	require.Equal(t, http.StatusOK, rr.Code)
	var challengeResp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&challengeResp))
	nonce := challengeResp["nonce"]

	// Sign with the wrong private key.
	badSig := signNonce(nonce, otherPriv)
	rr = postJSON(router, "/verify", models.VerifyRequest{
		PublicKey: pubBase64,
		Nonce:     nonce,
		Signature: badSig,
	})

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Equal(t, "Authentication failed", resp["error"])
}

func TestVerify_UnknownKey_Returns401(t *testing.T) {
	pubBase64, priv := generateEd25519KeyPair(t)
	nonceStore := newInMemoryNonceStore()

	store := &mockStore{
		insertAuthNonceFn:  nonceStore.insertFn,
		consumeAuthNonceFn: nonceStore.consumeFn,
		getUserByPublicKeyFn: func(_ context.Context, _ []byte) (*models.User, error) {
			return nil, errors.New("user not found")
		},
	}
	router := newTestRouter(store)

	rr := postJSON(router, "/challenge", models.ChallengeRequest{PublicKey: pubBase64})
	require.Equal(t, http.StatusOK, rr.Code)
	var challengeResp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&challengeResp))
	nonce := challengeResp["nonce"]

	sig := signNonce(nonce, priv)
	rr = postJSON(router, "/verify", models.VerifyRequest{
		PublicKey: pubBase64,
		Nonce:     nonce,
		Signature: sig,
	})

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Equal(t, "Authentication failed", resp["error"])
}

func TestVerify_InstanceBanned_Returns403(t *testing.T) {
	pubBase64, priv := generateEd25519KeyPair(t)
	user := newTestUser("alice")
	nonceStore := newInMemoryNonceStore()

	store := &mockStore{
		insertAuthNonceFn:  nonceStore.insertFn,
		consumeAuthNonceFn: nonceStore.consumeFn,
		getUserByPublicKeyFn: func(_ context.Context, _ []byte) (*models.User, error) {
			return user, nil
		},
		getActiveInstanceBanFn: func(_ context.Context, _ string) (*models.InstanceBan, error) {
			return &models.InstanceBan{
				ID:     uuid.New().String(),
				UserID: user.ID,
				Reason: "spam",
			}, nil
		},
	}
	router := newTestRouter(store)

	rr := postJSON(router, "/challenge", models.ChallengeRequest{PublicKey: pubBase64})
	require.Equal(t, http.StatusOK, rr.Code)
	var challengeResp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&challengeResp))
	nonce := challengeResp["nonce"]

	sig := signNonce(nonce, priv)
	rr = postJSON(router, "/verify", models.VerifyRequest{
		PublicKey: pubBase64,
		Nonce:     nonce,
		Signature: sig,
	})

	assert.Equal(t, http.StatusForbidden, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Contains(t, resp["error"], "banned")
}

