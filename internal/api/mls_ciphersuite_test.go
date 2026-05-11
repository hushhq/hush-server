package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/hushhq/hush-server/internal/models"
	"github.com/hushhq/hush-server/internal/version"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover the server-side write-boundary validator that requires
// every MLS state write to declare version.CurrentMLSCiphersuite explicitly.
// The validator runs after structural body checks (missing required fields)
// and after authentication, but before any DB accessor is invoked. Failures
// must produce a 400 response and must NOT call into the store.

const legacyMLSCiphersuite = 1 // 0x0001, MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519

// ── POST /credentials ────────────────────────────────────────────────────────

func TestMLS_UploadCredential_MissingCiphersuite_Returns400(t *testing.T) {
	store := &mockStore{}
	called := false
	store.upsertMLSCredentialFn = func(_ context.Context, _, _ string, _, _ []byte, _ int) error {
		called = true
		return nil
	}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	rr := postMLSRaw(router, "/credentials", token, map[string]interface{}{
		"deviceId":         "device1",
		"credentialBytes":  []byte("cred-bytes"),
		"signingPublicKey": []byte("sig-pub-key"),
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.False(t, called, "store must not be called when ciphersuite is missing")
}

func TestMLS_UploadCredential_LegacyCiphersuite_Returns400(t *testing.T) {
	store := &mockStore{}
	called := false
	store.upsertMLSCredentialFn = func(_ context.Context, _, _ string, _, _ []byte, _ int) error {
		called = true
		return nil
	}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	rr := postMLSRaw(router, "/credentials", token, map[string]interface{}{
		"deviceId":         "device1",
		"credentialBytes":  []byte("cred-bytes"),
		"signingPublicKey": []byte("sig-pub-key"),
		"ciphersuite":      legacyMLSCiphersuite,
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.False(t, called, "store must not be called when ciphersuite is legacy")

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "mls_ciphersuite_mismatch", body["error"])
}

func TestMLS_UploadCredential_CurrentCiphersuite_Returns204(t *testing.T) {
	store := &mockStore{}
	called := false
	store.upsertMLSCredentialFn = func(_ context.Context, _, _ string, _, _ []byte, _ int) error {
		called = true
		return nil
	}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	rr := postMLSRaw(router, "/credentials", token, map[string]interface{}{
		"deviceId":         "device1",
		"credentialBytes":  []byte("cred-bytes"),
		"signingPublicKey": []byte("sig-pub-key"),
		"ciphersuite":      version.CurrentMLSCiphersuite,
	})
	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.True(t, called, "store must be called for a current-suite request")
}

// ── POST /key-packages ───────────────────────────────────────────────────────

func TestMLS_UploadKeyPackages_MissingCiphersuite_Returns400(t *testing.T) {
	store := &mockStore{}
	called := false
	store.insertMLSKeyPackagesFn = func(_ context.Context, _, _ string, _ [][]byte, _ time.Time) error {
		called = true
		return nil
	}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	rr := postMLSRaw(router, "/key-packages", token, map[string]interface{}{
		"deviceId":    "device1",
		"keyPackages": [][]byte{[]byte("kp1")},
		"expiresAt":   time.Now().Add(time.Hour),
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.False(t, called)
}

func TestMLS_UploadKeyPackages_LegacyCiphersuite_Returns400(t *testing.T) {
	store := &mockStore{}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	rr := postMLSRaw(router, "/key-packages", token, map[string]interface{}{
		"deviceId":    "device1",
		"keyPackages": [][]byte{[]byte("kp1")},
		"expiresAt":   time.Now().Add(time.Hour),
		"ciphersuite": legacyMLSCiphersuite,
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestMLS_UploadKeyPackages_LastResort_LegacyCiphersuite_Returns400(t *testing.T) {
	store := &mockStore{}
	called := false
	store.insertMLSLastResortKeyPackageFn = func(_ context.Context, _, _ string, _ []byte) error {
		called = true
		return nil
	}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	rr := postMLSRaw(router, "/key-packages", token, map[string]interface{}{
		"deviceId":    "device1",
		"keyPackages": [][]byte{[]byte("lr")},
		"lastResort":  true,
		"ciphersuite": legacyMLSCiphersuite,
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.False(t, called)
}

// ── PUT /groups/:channelId/info ──────────────────────────────────────────────

func TestMLS_PutGroupInfo_MissingCiphersuite_Returns400(t *testing.T) {
	store := &mockStore{}
	called := false
	store.upsertMLSGroupInfoFn = func(_ context.Context, _, _ string, _ []byte, _ int64) error {
		called = true
		return nil
	}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	channelID := uuid.New().String()
	rr := putMLSRaw(router, "/groups/"+channelID+"/info", token, map[string]interface{}{
		"groupInfo": base64.StdEncoding.EncodeToString([]byte("gi")),
		"epoch":     int64(1),
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.False(t, called)
}

func TestMLS_PutGroupInfo_LegacyCiphersuite_Returns400(t *testing.T) {
	store := &mockStore{}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	channelID := uuid.New().String()
	rr := putMLSRaw(router, "/groups/"+channelID+"/info", token, map[string]interface{}{
		"groupInfo":   base64.StdEncoding.EncodeToString([]byte("gi")),
		"epoch":       int64(1),
		"ciphersuite": legacyMLSCiphersuite,
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ── POST /groups/:channelId/commit ───────────────────────────────────────────

func TestMLS_PostCommit_MissingCiphersuite_Returns400(t *testing.T) {
	store := &mockStore{}
	upserted := false
	appended := false
	store.upsertMLSGroupInfoFn = func(_ context.Context, _, _ string, _ []byte, _ int64) error {
		upserted = true
		return nil
	}
	store.appendMLSCommitFn = func(_ context.Context, _ string, _ int64, _ []byte, _ string) error {
		appended = true
		return nil
	}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	channelID := uuid.New().String()
	rr := postMLSRaw(router, "/groups/"+channelID+"/commit", token, map[string]interface{}{
		"commitBytes": base64.StdEncoding.EncodeToString([]byte("c")),
		"groupInfo":   base64.StdEncoding.EncodeToString([]byte("gi")),
		"epoch":       int64(1),
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.False(t, upserted, "must not write GroupInfo when ciphersuite missing")
	assert.False(t, appended, "must not append commit when ciphersuite missing")
}

func TestMLS_PostCommit_LegacyCiphersuite_Returns400(t *testing.T) {
	store := &mockStore{}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	channelID := uuid.New().String()
	rr := postMLSRaw(router, "/groups/"+channelID+"/commit", token, map[string]interface{}{
		"commitBytes": base64.StdEncoding.EncodeToString([]byte("c")),
		"groupInfo":   base64.StdEncoding.EncodeToString([]byte("gi")),
		"epoch":       int64(1),
		"ciphersuite": legacyMLSCiphersuite,
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ── PUT /guilds/:guildId/group-info ──────────────────────────────────────────

func TestMLS_PutGuildGroupInfo_MissingCiphersuite_Returns400(t *testing.T) {
	store := &mockStore{
		upsertMLSGuildMetadataGroupInfoFn: func(_ context.Context, _ string, _ []byte, _ int64) error {
			return nil
		},
		getServerMemberLevelFn: func(_ context.Context, _, _ string) (int, error) {
			return models.PermissionLevelAdmin, nil
		},
	}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	guildID := uuid.New().String()
	rr := putMLSRaw(router, "/guilds/"+guildID+"/group-info", token, map[string]interface{}{
		"groupInfo": base64.StdEncoding.EncodeToString([]byte("gi")),
		"epoch":     int64(1),
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestMLS_PutGuildGroupInfo_LegacyCiphersuite_Returns400(t *testing.T) {
	store := &mockStore{
		upsertMLSGuildMetadataGroupInfoFn: func(_ context.Context, _ string, _ []byte, _ int64) error {
			return nil
		},
		getServerMemberLevelFn: func(_ context.Context, _, _ string) (int, error) {
			return models.PermissionLevelAdmin, nil
		},
	}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	guildID := uuid.New().String()
	rr := putMLSRaw(router, "/guilds/"+guildID+"/group-info", token, map[string]interface{}{
		"groupInfo":   base64.StdEncoding.EncodeToString([]byte("gi")),
		"epoch":       int64(1),
		"ciphersuite": legacyMLSCiphersuite,
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
