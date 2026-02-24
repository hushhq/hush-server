package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"hush.app/server/internal/auth"
	"hush.app/server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func keysRouter(store *mockStore, hub KeysBroadcaster) http.Handler {
	return KeysRoutes(store, hub, testJWTSecret)
}

func postKeysUpload(handler http.Handler, token string, body interface{}) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func getKeys(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestKeysUpload_ValidRequest_ReturnsSuccess(t *testing.T) {
	userID := uuid.New().String()
	sessionID := uuid.New().String()
	token, err := auth.SignJWT(userID, sessionID, testJWTSecret, time.Now().Add(time.Hour))
	require.NoError(t, err)
	tokenHash := auth.TokenHash(token)

	store := &mockStore{
		getSessionByTokenHashFn: func(_ context.Context, th string) (*models.Session, error) {
			if th != tokenHash {
				return nil, nil
			}
			return &models.Session{ID: sessionID, UserID: userID, TokenHash: th, ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
	}
	hub := &mockKeysHub{}
	router := keysRouter(store, hub)

	rr := postKeysUpload(router, token, models.PreKeyUploadRequest{
		DeviceID:             "device1",
		IdentityKey:          []byte("identity32bytesxxxxxxxxxxxxxxxx"),
		SignedPreKey:         []byte("signed32bytesxxxxxxxxxxxxxxxxx"),
		SignedPreKeySignature: []byte("sig64bytesxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"),
		RegistrationID:       12345,
		OneTimePreKeys:       []models.OneTimePreKeyRow{{KeyID: 1, PublicKey: []byte("pk32bytesxxxxxxxxxxxxxxxxxxxx")}},
	})

	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestKeysUpload_MissingAuth_Returns401(t *testing.T) {
	store := &mockStore{}
	router := keysRouter(store, &mockKeysHub{})

	rr := postKeysUpload(router, "", models.PreKeyUploadRequest{
		DeviceID:             "d1",
		IdentityKey:          []byte("identity32bytesxxxxxxxxxxxxxxxx"),
		SignedPreKey:         []byte("signed32bytesxxxxxxxxxxxxxxxxx"),
		SignedPreKeySignature: []byte("sig64bytesxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"),
		RegistrationID:       1,
	})

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestKeysUpload_InvalidBody_Returns400(t *testing.T) {
	userID := uuid.New().String()
	sessionID := uuid.New().String()
	token, _ := auth.SignJWT(userID, sessionID, testJWTSecret, time.Now().Add(time.Hour))
	tokenHash := auth.TokenHash(token)
	store := &mockStore{
		getSessionByTokenHashFn: func(_ context.Context, th string) (*models.Session, error) {
			if th != tokenHash {
				return nil, nil
			}
			return &models.Session{ID: sessionID, UserID: userID, ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
	}
	router := keysRouter(store, &mockKeysHub{})

	rr := postKeysUpload(router, token, map[string]string{"deviceId": ""})
	assert.Equal(t, http.StatusBadRequest, rr.Code)

	rr2 := postKeysUpload(router, token, models.PreKeyUploadRequest{DeviceID: "d1"})
	assert.Equal(t, http.StatusBadRequest, rr2.Code)
}

func TestKeysGetByUser_ValidUser_ReturnsBundles(t *testing.T) {
	callerID := uuid.New().String()
	targetID := uuid.New().String()
	sessionID := uuid.New().String()
	token, _ := auth.SignJWT(callerID, sessionID, testJWTSecret, time.Now().Add(time.Hour))
	tokenHash := auth.TokenHash(token)

	identityKey := []byte("identity32bytesxxxxxxxxxxxxxxxx")
	signedPreKey := []byte("signed32bytesxxxxxxxxxxxxxxxxx")
	sig := []byte("sig64bytesxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")

	store := &mockStore{
		getSessionByTokenHashFn: func(_ context.Context, th string) (*models.Session, error) {
			if th != tokenHash {
				return nil, nil
			}
			return &models.Session{ID: sessionID, UserID: callerID, ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
		listDeviceIDsForUserFn: func(_ context.Context, uid string) ([]string, error) {
			if uid != targetID {
				return nil, nil
			}
			return []string{"dev1"}, nil
		},
		getIdentityAndSignedPreKeyFn: func(_ context.Context, uid, did string) ([]byte, []byte, []byte, int, error) {
			if uid == targetID && did == "dev1" {
				return identityKey, signedPreKey, sig, 99, nil
			}
			return nil, nil, nil, 0, nil
		},
		consumeOneTimePreKeyFn: func(_ context.Context, uid, did string) (int, []byte, error) {
			if uid == targetID && did == "dev1" {
				return 1, []byte("otpk32bytesxxxxxxxxxxxxxxxx"), nil
			}
			return 0, nil, nil
		},
	}
	router := keysRouter(store, &mockKeysHub{})

	rr := getKeys(router, "/"+targetID, token)
	require.Equal(t, http.StatusOK, rr.Code)
	var bundles []models.PreKeyBundle
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&bundles))
	require.Len(t, bundles, 1)
	assert.Equal(t, identityKey, bundles[0].IdentityKey)
	assert.Equal(t, signedPreKey, bundles[0].SignedPreKey)
	require.NotNil(t, bundles[0].OneTimePreKeyID)
	assert.Equal(t, 1, *bundles[0].OneTimePreKeyID)
}

func TestKeysGetByUser_NoKeys_ReturnsEmpty(t *testing.T) {
	callerID := uuid.New().String()
	sessionID := uuid.New().String()
	targetID := uuid.New().String()
	token, _ := auth.SignJWT(callerID, sessionID, testJWTSecret, time.Now().Add(time.Hour))
	tokenHash := auth.TokenHash(token)
	store := &mockStore{
		getSessionByTokenHashFn: func(_ context.Context, th string) (*models.Session, error) {
			if th != tokenHash {
				return nil, nil
			}
			return &models.Session{ID: sessionID, UserID: callerID, ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
		listDeviceIDsForUserFn: func(_ context.Context, _ string) ([]string, error) {
			return nil, nil
		},
	}
	router := keysRouter(store, &mockKeysHub{})

	rr := getKeys(router, "/"+targetID, token)
	assert.Equal(t, http.StatusOK, rr.Code)
	var bundles []models.PreKeyBundle
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&bundles))
	assert.Empty(t, bundles)
}

func TestKeysGetByUserDevice_Valid_ReturnsSingleBundle(t *testing.T) {
	callerID := uuid.New().String()
	sessionID := uuid.New().String()
	targetID := uuid.New().String()
	token, _ := auth.SignJWT(callerID, sessionID, testJWTSecret, time.Now().Add(time.Hour))
	tokenHash := auth.TokenHash(token)
	identityKey := []byte("identity32bytesxxxxxxxxxxxxxxxx")
	store := &mockStore{
		getSessionByTokenHashFn: func(_ context.Context, th string) (*models.Session, error) {
			if th != tokenHash {
				return nil, nil
			}
			return &models.Session{ID: sessionID, UserID: callerID, ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
		getIdentityAndSignedPreKeyFn: func(_ context.Context, uid, did string) ([]byte, []byte, []byte, int, error) {
			if uid == targetID && did == "dev1" {
				return identityKey, []byte("spk"), []byte("sig"), 1, nil
			}
			return nil, nil, nil, 0, nil
		},
		consumeOneTimePreKeyFn: func(_ context.Context, _, _ string) (int, []byte, error) {
			return 2, []byte("otpk"), nil
		},
	}
	router := keysRouter(store, &mockKeysHub{})

	rr := getKeys(router, "/"+targetID+"/dev1", token)
	require.Equal(t, http.StatusOK, rr.Code)
	var bundle models.PreKeyBundle
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&bundle))
	assert.Equal(t, identityKey, bundle.IdentityKey)
	require.NotNil(t, bundle.OneTimePreKeyID)
	assert.Equal(t, 2, *bundle.OneTimePreKeyID)
}

func TestKeysGetByUserDevice_InvalidUserID_Returns400(t *testing.T) {
	userID := uuid.New().String()
	sessionID := uuid.New().String()
	token, _ := auth.SignJWT(userID, sessionID, testJWTSecret, time.Now().Add(time.Hour))
	tokenHash := auth.TokenHash(token)
	store := &mockStore{
		getSessionByTokenHashFn: func(_ context.Context, th string) (*models.Session, error) {
			if th != tokenHash {
				return nil, nil
			}
			return &models.Session{ID: sessionID, UserID: userID, ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
	}
	router := keysRouter(store, &mockKeysHub{})

	rr := getKeys(router, "/not-a-uuid/dev1", token)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestKeys_LowPreKeys_AfterRetrieve_BroadcastsKeysLow(t *testing.T) {
	callerID := uuid.New().String()
	sessionID := uuid.New().String()
	targetID := uuid.New().String()
	token, _ := auth.SignJWT(callerID, sessionID, testJWTSecret, time.Now().Add(time.Hour))
	tokenHash := auth.TokenHash(token)
	hub := &mockKeysHub{}

	store := &mockStore{
		getSessionByTokenHashFn: func(_ context.Context, th string) (*models.Session, error) {
			if th != tokenHash {
				return nil, nil
			}
			return &models.Session{ID: sessionID, UserID: callerID, ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
		listDeviceIDsForUserFn: func(_ context.Context, uid string) ([]string, error) {
			if uid == targetID {
				return []string{"dev1"}, nil
			}
			return nil, nil
		},
		getIdentityAndSignedPreKeyFn: func(_ context.Context, uid, did string) ([]byte, []byte, []byte, int, error) {
			if uid == targetID && did == "dev1" {
				return []byte("ik"), []byte("spk"), []byte("sig"), 1, nil
			}
			return nil, nil, nil, 0, nil
		},
		consumeOneTimePreKeyFn: func(_ context.Context, uid, did string) (int, []byte, error) {
			if uid == targetID && did == "dev1" {
				return 1, []byte("otpk"), nil
			}
			return 0, nil, nil
		},
		countUnusedOneTimePreKeysFn: func(_ context.Context, uid, did string) (int, error) {
			if uid == targetID && did == "dev1" {
				return 9, nil
			}
			return 0, nil
		},
	}
	router := keysRouter(store, hub)

	rr := getKeys(router, "/"+targetID, token)
	require.Equal(t, http.StatusOK, rr.Code)

	hub.mu.Lock()
	calls := hub.broadcastCalls
	hub.mu.Unlock()
	require.Len(t, calls, 1)
	assert.Equal(t, targetID, calls[0].userID)
	var payload struct {
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(calls[0].message, &payload))
	assert.Equal(t, "keys.low", payload.Type)
}

type mockKeysHub struct {
	mu            sync.Mutex
	broadcastCalls []struct{ userID string; message []byte }
}

func (m *mockKeysHub) BroadcastToUser(userID string, message []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broadcastCalls = append(m.broadcastCalls, struct{ userID string; message []byte }{userID, message})
}
