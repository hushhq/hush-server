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

	internalauth "github.com/hushhq/hush-server/internal/auth"
	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/models"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

func get(handler http.Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
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

// ---------- Check Username ----------

func TestCheckUsername_NoRows_ReturnsAvailableTrue(t *testing.T) {
	store := &mockStore{
		getUserByUsernameFn: func(_ context.Context, username string) (*models.User, error) {
			assert.Equal(t, "alice", username)
			return nil, pgx.ErrNoRows
		},
	}
	router := newTestRouter(store)

	rr := get(router, "/check-username/alice")

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"available":true}`, rr.Body.String())
}

func TestCheckUsername_UserExists_ReturnsAvailableFalse(t *testing.T) {
	store := &mockStore{
		getUserByUsernameFn: func(_ context.Context, username string) (*models.User, error) {
			assert.Equal(t, "alice", username)
			return newTestUser(username), nil
		},
	}
	router := newTestRouter(store)

	rr := get(router, "/check-username/alice")

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.JSONEq(t, `{"available":false}`, rr.Body.String())
}

func TestCheckUsername_DatabaseError_Returns500(t *testing.T) {
	store := &mockStore{
		getUserByUsernameFn: func(_ context.Context, _ string) (*models.User, error) {
			return nil, errors.New("database offline")
		},
	}
	router := newTestRouter(store)

	rr := get(router, "/check-username/alice")

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Equal(t, "username check failed", resp["error"])
}

// ---------- Register ----------

func TestRegister_Success(t *testing.T) {
	pubBase64, _ := generateEd25519KeyPair(t)
	user := newTestUser("alice")

	store := &mockStore{
		createUserWithPublicKeyFn: func(_ context.Context, username, displayName string, _ []byte) (*models.User, error) {
			assert.Equal(t, "alice", username)
			assert.Equal(t, "", displayName)
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

func TestRegister_PersistsDeviceLabel(t *testing.T) {
	pubBase64, _ := generateEd25519KeyPair(t)
	pubBytes, err := base64.StdEncoding.DecodeString(pubBase64)
	require.NoError(t, err)
	user := newTestUser("alice")
	var insertedLabel string

	store := &mockStore{
		createUserWithPublicKeyFn: func(_ context.Context, username, displayName string, _ []byte) (*models.User, error) {
			return user, nil
		},
		insertDeviceKeyFn: func(_ context.Context, userID, deviceID, label string, devicePublicKey, certificate []byte) error {
			assert.Equal(t, user.ID, userID)
			assert.Equal(t, "device-1", deviceID)
			assert.Equal(t, "Chrome on macOS", label)
			assert.Equal(t, pubBytes, devicePublicKey)
			assert.Nil(t, certificate)
			insertedLabel = label
			return nil
		},
	}
	router := newTestRouter(store)

	rr := postJSON(router, "/register", models.RegisterRequest{
		Username:  "alice",
		PublicKey: pubBase64,
		DeviceID:  "device-1",
		Label:     "Chrome on macOS",
	})

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "Chrome on macOS", insertedLabel)
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

func TestRegister_InstanceUserLimitReached_Returns403(t *testing.T) {
	pubBase64, _ := generateEd25519KeyPair(t)
	store := &mockStore{
		createUserWithPublicKeyFn: func(_ context.Context, _, _ string, _ []byte) (*models.User, error) {
			return nil, db.ErrInstanceUserLimitReached
		},
	}
	router := newTestRouter(store)

	rr := postJSON(router, "/register", models.RegisterRequest{
		Username:  "alice",
		PublicKey: pubBase64,
	})

	assert.Equal(t, http.StatusForbidden, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Equal(t, "Registration limit reached", resp["error"])
}

func TestRegister_AccountRecovery_ExistingPublicKey_ReturnsAuthResponse(t *testing.T) {
	pubBase64, _ := generateEd25519KeyPair(t)
	existingUser := newTestUser("alice")

	store := &mockStore{
		// Probe finds the existing user - account recovery path taken.
		getUserByPublicKeyFn: func(_ context.Context, _ []byte) (*models.User, error) {
			return existingUser, nil
		},
		// CreateUserWithPublicKey must NOT be called in the recovery path.
		createUserWithPublicKeyFn: func(_ context.Context, _, _ string, _ []byte) (*models.User, error) {
			return nil, errors.New("should not be called in account recovery path")
		},
	}
	router := newTestRouter(store)

	rr := postJSON(router, "/register", models.RegisterRequest{
		Username:  "alice",
		PublicKey: pubBase64,
	})

	// Recovery path returns 200 OK with a valid auth response (same as normal register).
	assert.Equal(t, http.StatusOK, rr.Code)
	resp := decodeAuthResponse(t, rr)
	assert.NotEmpty(t, resp.Token)
	assert.Equal(t, existingUser.ID, resp.User.ID)
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

// TestRegister_BannedUsername_Returns403 verifies IROLE-03: a banned user
// cannot re-register with the same username on this instance.
//
// IMPLEMENTATION GAP (escalated): register() in auth.go does not call
// GetActiveInstanceBan or GetUserByUsername before CreateUserWithPublicKey.
// Only verify() checks for active bans. The ban check described in 0G-01-PLAN.md
// was never implemented for the register path.
//
// Expected when fixed: 403 with error containing "Registration blocked".
// Actual currently: falls through to CreateUserWithPublicKey (no ban check).
//
// This test is marked as a known-failing escalation. The username lookup and
// ban check are wired in the mock; the absence of a 403 response proves the
// implementation gap.
func TestRegister_BannedUsername_Returns403(t *testing.T) {
	pubBase64, _ := generateEd25519KeyPair(t)
	bannedUser := newTestUser("alice")

	banCheckCalled := false
	store := &mockStore{
		// Probe for existing public key finds no match (different device/key).
		getUserByPublicKeyFn: func(_ context.Context, _ []byte) (*models.User, error) {
			return nil, nil
		},
		// Username lookup returns the banned user's record.
		getUserByUsernameFn: func(_ context.Context, username string) (*models.User, error) {
			if username == "alice" {
				return bannedUser, nil
			}
			return nil, nil
		},
		// Active ban exists for the banned user.
		getActiveInstanceBanFn: func(_ context.Context, userID string) (*models.InstanceBan, error) {
			banCheckCalled = true
			if userID == bannedUser.ID {
				return &models.InstanceBan{
					ID:     "ban-1",
					UserID: bannedUser.ID,
					Reason: "spam",
				}, nil
			}
			return nil, nil
		},
		getInstanceConfigFn: func(_ context.Context) (*models.InstanceConfig, error) {
			return &models.InstanceConfig{
				ID:               "inst-1",
				RegistrationMode: "open",
			}, nil
		},
		// Stub CreateUserWithPublicKey to prevent nil-dereference panic when the
		// ban check is absent and execution falls through.
		createUserWithPublicKeyFn: func(_ context.Context, _, _ string, _ []byte) (*models.User, error) {
			return newTestUser("alice"), nil
		},
	}
	router := newTestRouter(store)

	rr := postJSON(router, "/register", models.RegisterRequest{
		Username:  "alice",
		PublicKey: pubBase64,
	})

	// When the implementation is correct: 403 with "Registration blocked".
	// When the implementation is missing the ban check: 200 OK (falls through).
	if rr.Code == http.StatusOK {
		// Confirm the ban check was not called - proving the implementation gap.
		assert.False(t, banCheckCalled, "GetActiveInstanceBan was unexpectedly called on register path")
		t.Fatalf("IMPLEMENTATION GAP (IROLE-03): register() returned 200 for a banned username; " +
			"expected 403. GetUserByUsername + GetActiveInstanceBan are not called in register(). " +
			"Fix: add ban check in auth.go register() before CreateUserWithPublicKey. " +
			"See 0G-01-PLAN.md Task 2 auth.go section.")
	}
	assert.Equal(t, http.StatusForbidden, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Contains(t, resp["error"], "Registration blocked")
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

func TestVerify_BackfillsDeviceKey_WhenDeviceIDProvided(t *testing.T) {
	pubBase64, priv := generateEd25519KeyPair(t)
	user := newTestUser("alice")
	nonceStore := newInMemoryNonceStore()

	var backfilledUserID string
	var backfilledDeviceID string
	var backfilledPublicKey []byte
	var upsertedDeviceUserID string
	var upsertedDeviceID string

	store := &mockStore{
		insertAuthNonceFn:  nonceStore.insertFn,
		consumeAuthNonceFn: nonceStore.consumeFn,
		getUserByPublicKeyFn: func(_ context.Context, _ []byte) (*models.User, error) {
			return user, nil
		},
		// ans23 / F5: /verify backfill now goes through
		// BackfillRootDeviceKey, never the upsert-on-conflict path.
		backfillRootDeviceKeyFn: func(_ context.Context, userID, deviceID string, devicePublicKey []byte) (bool, error) {
			backfilledUserID = userID
			backfilledDeviceID = deviceID
			backfilledPublicKey = append([]byte(nil), devicePublicKey...)
			return true, nil
		},
		insertDeviceKeyFn: func(context.Context, string, string, string, []byte, []byte) error {
			t.Fatalf("InsertDeviceKey must not be called from /verify backfill")
			return nil
		},
		upsertDeviceFn: func(_ context.Context, userID, deviceID, label string) error {
			upsertedDeviceUserID = userID
			upsertedDeviceID = deviceID
			require.Empty(t, label)
			return nil
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
		DeviceID:  "device-backfill-1",
	})

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, user.ID, backfilledUserID)
	assert.Equal(t, "device-backfill-1", backfilledDeviceID)
	assert.Equal(t, user.ID, upsertedDeviceUserID)
	assert.Equal(t, "device-backfill-1", upsertedDeviceID)

	expectedPublicKey, err := base64.StdEncoding.DecodeString(pubBase64)
	require.NoError(t, err)
	assert.Equal(t, expectedPublicKey, backfilledPublicKey)
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

func TestVerify_UnknownPublicKey_Returns404(t *testing.T) {
	pubBase64, priv := generateEd25519KeyPair(t)
	nonceStore := newInMemoryNonceStore()

	// getUserByPublicKeyFn returns (nil, nil): key not found, no DB error.
	store := &mockStore{
		insertAuthNonceFn:  nonceStore.insertFn,
		consumeAuthNonceFn: nonceStore.consumeFn,
		getUserByPublicKeyFn: func(_ context.Context, _ []byte) (*models.User, error) {
			return nil, nil
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

	assert.Equal(t, http.StatusNotFound, rr.Code)
	resp := decodeErrorResponse(t, rr)
	assert.Equal(t, "unknown public key", resp["error"])
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

// ---------- Guest Auth ----------

func TestGuestAuth_IssuesShortLivedToken(t *testing.T) {
	store := &mockStore{}
	router := newTestRouter(store)

	rr := postJSON(router, "/guest", nil)

	require.Equal(t, http.StatusOK, rr.Code)

	var resp models.GuestAuthResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))

	assert.NotEmpty(t, resp.Token, "token must be non-empty")
	assert.NotEmpty(t, resp.GuestID, "guestId must be non-empty")
	assert.True(t, resp.ExpiresAt.After(time.Now()), "expiresAt must be in the future")

	// Token must validate and carry is_guest=true.
	_, _, _, isGuest, _, _, err := internalauth.ValidateJWT(resp.Token, testJWTSecret)
	require.NoError(t, err)
	assert.True(t, isGuest, "token must have is_guest=true")
}

func TestGuestAuth_GuestIDPrefixedWithGuest(t *testing.T) {
	store := &mockStore{}
	router := newTestRouter(store)

	rr := postJSON(router, "/guest", nil)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp models.GuestAuthResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))

	assert.True(t, strings.HasPrefix(resp.GuestID, "guest_"), "guestId must start with 'guest_'")
}

func TestGuestAuth_ExpiryWithinExpectedRange(t *testing.T) {
	store := &mockStore{}
	router := newTestRouter(store)

	before := time.Now()
	rr := postJSON(router, "/guest", nil)
	after := time.Now()

	require.Equal(t, http.StatusOK, rr.Code)

	var resp models.GuestAuthResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))

	// Default GUEST_SESSION_HOURS=1, test config uses 1h JWT expiry.
	// ExpiresAt should be approximately now + 1 hour (within a 5-second window).
	minExpiry := before.Add(time.Hour - 5*time.Second)
	maxExpiry := after.Add(time.Hour + 5*time.Second)
	assert.True(t, resp.ExpiresAt.After(minExpiry), "expiresAt too early")
	assert.True(t, resp.ExpiresAt.Before(maxExpiry), "expiresAt too late")
}
