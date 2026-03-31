package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mlsRouter builds the MLS chi router with the given store and hub.
func mlsRouter(store *mockStore, hub MLSBroadcaster) http.Handler {
	return MLSRoutes(store, hub, testJWTSecret, nil)
}

func postMLS(handler http.Handler, path, token string, body interface{}) *httptest.ResponseRecorder {
	var buf []byte
	if body != nil {
		var err error
		buf, err = json.Marshal(body)
		if err != nil {
			panic(err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func getMLS(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// ---------- POST /credentials ----------

func TestMLS_UploadCredential_ValidRequest_Returns204(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	var storedUserID, storedDeviceID string
	var storedCredBytes, storedSigKey []byte
	store.upsertMLSCredentialFn = func(_ context.Context, uid, did string, cred, sig []byte, _ int) error {
		storedUserID = uid
		storedDeviceID = did
		storedCredBytes = cred
		storedSigKey = sig
		return nil
	}
	router := mlsRouter(store, &mockMLSHub{})

	rr := postMLS(router, "/credentials", token, map[string]interface{}{
		"deviceId":        "device1",
		"credentialBytes": []byte("cred-bytes"),
		"signingPublicKey": []byte("sig-pub-key"),
	})

	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.Equal(t, userID, storedUserID)
	assert.Equal(t, "device1", storedDeviceID)
	assert.Equal(t, []byte("cred-bytes"), storedCredBytes)
	assert.Equal(t, []byte("sig-pub-key"), storedSigKey)
}

func TestMLS_UploadCredential_EmptyCredentialBytes_Returns400(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)
	router := mlsRouter(store, &mockMLSHub{})

	rr := postMLS(router, "/credentials", token, map[string]interface{}{
		"deviceId":        "device1",
		"credentialBytes": []byte{},
		"signingPublicKey": []byte("sig-pub-key"),
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestMLS_UploadCredential_MissingDeviceID_Returns400(t *testing.T) {
	store := &mockStore{}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	rr := postMLS(router, "/credentials", token, map[string]interface{}{
		"deviceId":        "",
		"credentialBytes": []byte("cred"),
		"signingPublicKey": []byte("sig"),
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestMLS_UploadCredential_NoAuth_Returns401(t *testing.T) {
	store := &mockStore{}
	router := mlsRouter(store, &mockMLSHub{})

	rr := postMLS(router, "/credentials", "", map[string]interface{}{
		"deviceId":        "device1",
		"credentialBytes": []byte("cred"),
		"signingPublicKey": []byte("sig"),
	})
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// ---------- POST /key-packages ----------

func TestMLS_UploadKeyPackages_ValidBatch_Returns204(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	var storedPackages [][]byte
	store.insertMLSKeyPackagesFn = func(_ context.Context, _, _ string, pkgs [][]byte, _ time.Time) error {
		storedPackages = pkgs
		return nil
	}
	router := mlsRouter(store, &mockMLSHub{})

	expiresAt := time.Now().Add(30 * 24 * time.Hour)
	rr := postMLS(router, "/key-packages", token, map[string]interface{}{
		"deviceId":    "device1",
		"keyPackages": [][]byte{[]byte("pkg1"), []byte("pkg2"), []byte("pkg3")},
		"expiresAt":   expiresAt,
		"lastResort":  false,
	})

	require.Equal(t, http.StatusNoContent, rr.Code)
	require.Len(t, storedPackages, 3)
}

func TestMLS_UploadKeyPackages_LastResort_UsesLastResortStore(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	lastResortCalled := false
	var lastResortBytes []byte
	store.insertMLSLastResortKeyPackageFn = func(_ context.Context, _, _ string, kpBytes []byte) error {
		lastResortCalled = true
		lastResortBytes = kpBytes
		return nil
	}
	regularCalled := false
	store.insertMLSKeyPackagesFn = func(_ context.Context, _, _ string, _ [][]byte, _ time.Time) error {
		regularCalled = true
		return nil
	}
	router := mlsRouter(store, &mockMLSHub{})

	rr := postMLS(router, "/key-packages", token, map[string]interface{}{
		"deviceId":    "device1",
		"keyPackages": [][]byte{[]byte("last-resort-pkg")},
		"expiresAt":   time.Now().Add(30 * 24 * time.Hour),
		"lastResort":  true,
	})

	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.True(t, lastResortCalled, "InsertMLSLastResortKeyPackage should have been called")
	assert.Equal(t, []byte("last-resort-pkg"), lastResortBytes)
	assert.False(t, regularCalled, "InsertMLSKeyPackages should NOT have been called for last-resort")
}

func TestMLS_UploadKeyPackages_EmptyPackages_Returns400(t *testing.T) {
	store := &mockStore{}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	rr := postMLS(router, "/key-packages", token, map[string]interface{}{
		"deviceId":    "device1",
		"keyPackages": [][]byte{},
		"expiresAt":   time.Now().Add(30 * 24 * time.Hour),
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestMLS_UploadKeyPackages_TooManyPackages_Returns400(t *testing.T) {
	store := &mockStore{}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	pkgs := make([][]byte, 201)
	for i := range pkgs {
		pkgs[i] = []byte("pkg")
	}
	rr := postMLS(router, "/key-packages", token, map[string]interface{}{
		"deviceId":    "device1",
		"keyPackages": pkgs,
		"expiresAt":   time.Now().Add(30 * 24 * time.Hour),
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------- GET /key-packages/count ----------

func TestMLS_GetKeyPackageCount_Returns_Count(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)
	store.countUnusedMLSKeyPackagesFn = func(_ context.Context, uid, did string) (int, error) {
		if uid == userID && did == "device1" {
			return 42, nil
		}
		return 0, nil
	}
	router := mlsRouter(store, &mockMLSHub{})

	rr := getMLS(router, "/key-packages/count?deviceId=device1", token)
	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]int
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, 42, resp["count"])
}

func TestMLS_GetKeyPackageCount_MissingDeviceID_Returns400(t *testing.T) {
	store := &mockStore{}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	rr := getMLS(router, "/key-packages/count", token)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestMLS_GetKeyPackageCount_NoAuth_Returns401(t *testing.T) {
	store := &mockStore{}
	router := mlsRouter(store, &mockMLSHub{})

	rr := getMLS(router, "/key-packages/count?deviceId=device1", "")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// ---------- GET /key-packages/:userId/:deviceId ----------

func TestMLS_ConsumeKeyPackage_Returns_Package(t *testing.T) {
	store := &mockStore{}
	callerID := uuid.New().String()
	targetID := uuid.New().String()
	token := makeAuth(store, callerID)

	kpBytes := []byte("key-package-data")
	store.consumeMLSKeyPackageFn = func(_ context.Context, uid, did string) ([]byte, error) {
		if uid == targetID && did == "device1" {
			return kpBytes, nil
		}
		return nil, nil
	}
	store.countUnusedMLSKeyPackagesFn = func(_ context.Context, uid, _ string) (int, error) {
		return 50, nil // above threshold — no low event
	}
	router := mlsRouter(store, &mockMLSHub{})

	rr := getMLS(router, "/key-packages/"+targetID+"/device1", token)
	require.Equal(t, http.StatusOK, rr.Code)
	var resp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.NotEmpty(t, resp["keyPackage"], "keyPackage field should be present and non-empty")
}

func TestMLS_ConsumeKeyPackage_NoPackageAvailable_Returns404(t *testing.T) {
	store := &mockStore{}
	callerID := uuid.New().String()
	targetID := uuid.New().String()
	token := makeAuth(store, callerID)

	store.consumeMLSKeyPackageFn = func(_ context.Context, _, _ string) ([]byte, error) {
		return nil, nil // no package
	}
	router := mlsRouter(store, &mockMLSHub{})

	rr := getMLS(router, "/key-packages/"+targetID+"/device1", token)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestMLS_ConsumeKeyPackage_InvalidUserID_Returns400(t *testing.T) {
	store := &mockStore{}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	rr := getMLS(router, "/key-packages/not-a-uuid/device1", token)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestMLS_ConsumeKeyPackage_FiresLowEvent_WhenCountBelowThreshold(t *testing.T) {
	store := &mockStore{}
	callerID := uuid.New().String()
	targetID := uuid.New().String()
	token := makeAuth(store, callerID)
	hub := &mockMLSHub{}

	kpBytes := []byte("key-package-data")
	store.consumeMLSKeyPackageFn = func(_ context.Context, uid, _ string) ([]byte, error) {
		if uid == targetID {
			return kpBytes, nil
		}
		return nil, nil
	}
	store.countUnusedMLSKeyPackagesFn = func(_ context.Context, uid, _ string) (int, error) {
		if uid == targetID {
			return 5, nil // below threshold of 10
		}
		return 50, nil
	}
	router := mlsRouter(store, hub)

	rr := getMLS(router, "/key-packages/"+targetID+"/device1", token)
	require.Equal(t, http.StatusOK, rr.Code)

	// Give the goroutine time to fire.
	time.Sleep(50 * time.Millisecond)

	hub.mu.Lock()
	calls := hub.broadcastCalls
	hub.mu.Unlock()

	require.Len(t, calls, 1)
	assert.Equal(t, targetID, calls[0].userID)
	var payload struct {
		Type string `json:"type"`
	}
	require.NoError(t, json.Unmarshal(calls[0].message, &payload))
	assert.Equal(t, "key_packages.low", payload.Type)
}

func TestMLS_ConsumeKeyPackage_NoLowEvent_WhenCountAboveThreshold(t *testing.T) {
	store := &mockStore{}
	callerID := uuid.New().String()
	targetID := uuid.New().String()
	token := makeAuth(store, callerID)
	hub := &mockMLSHub{}

	store.consumeMLSKeyPackageFn = func(_ context.Context, _, _ string) ([]byte, error) {
		return []byte("kp"), nil
	}
	store.countUnusedMLSKeyPackagesFn = func(_ context.Context, _, _ string) (int, error) {
		return 50, nil // above threshold
	}
	router := mlsRouter(store, hub)

	rr := getMLS(router, "/key-packages/"+targetID+"/device1", token)
	require.Equal(t, http.StatusOK, rr.Code)

	time.Sleep(50 * time.Millisecond)

	hub.mu.Lock()
	calls := hub.broadcastCalls
	hub.mu.Unlock()
	assert.Empty(t, calls, "no key_packages.low event should fire when count is above threshold")
}

// ---------- Handshake: key_package_low_threshold ----------

func TestHandshake_ContainsKeyPackageLowThreshold(t *testing.T) {
	cache := NewInstanceCache()
	handler := HandshakeHandler(cache, false)

	req := httptest.NewRequest(http.MethodGet, "/api/handshake", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Contains(t, resp, "key_package_low_threshold", "handshake should contain key_package_low_threshold, not opk_low_threshold")
	assert.NotContains(t, resp, "opk_low_threshold", "old Signal field opk_low_threshold must not appear")
}

// ---------- Group info helpers ----------

func putMLS(handler http.Handler, path, token string, body interface{}) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func deleteMLS(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// ---------- GET /groups/:channelId/info ----------

func TestMLS_GetGroupInfo_Found_Returns200(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	channelID := uuid.New().String()
	token := makeAuth(store, userID)

	groupBytes := []byte("group-info-bytes")
	store.getMLSGroupInfoFn = func(_ context.Context, cid string, _ string) ([]byte, int64, error) {
		if cid == channelID {
			return groupBytes, 7, nil
		}
		return nil, 0, nil
	}
	router := mlsRouter(store, &mockMLSHub{})

	rr := getMLS(router, "/groups/"+channelID+"/info", token)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp struct {
		GroupInfo string `json:"groupInfo"`
		Epoch     int64  `json:"epoch"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.NotEmpty(t, resp.GroupInfo)
	assert.Equal(t, int64(7), resp.Epoch)
}

func TestMLS_GetGroupInfo_NotFound_Returns404(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	channelID := uuid.New().String()
	token := makeAuth(store, userID)

	store.getMLSGroupInfoFn = func(_ context.Context, _ string, _ string) ([]byte, int64, error) {
		return nil, 0, nil
	}
	router := mlsRouter(store, &mockMLSHub{})

	rr := getMLS(router, "/groups/"+channelID+"/info", token)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// ---------- PUT /groups/:channelId/info ----------

func TestMLS_PutGroupInfo_Valid_Returns204(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	channelID := uuid.New().String()
	token := makeAuth(store, userID)

	var upsertedBytes []byte
	var upsertedEpoch int64
	store.upsertMLSGroupInfoFn = func(_ context.Context, _ string, _ string, b []byte, epoch int64) error {
		upsertedBytes = b
		upsertedEpoch = epoch
		return nil
	}
	router := mlsRouter(store, &mockMLSHub{})

	encoded := base64.StdEncoding.EncodeToString([]byte("group-info-bytes"))
	rr := putMLS(router, "/groups/"+channelID+"/info", token, map[string]interface{}{
		"groupInfo": encoded,
		"epoch":     int64(3),
	})
	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.Equal(t, []byte("group-info-bytes"), upsertedBytes)
	assert.Equal(t, int64(3), upsertedEpoch)
}

func TestMLS_PutGroupInfo_EmptyBytes_Returns400(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	channelID := uuid.New().String()
	token := makeAuth(store, userID)
	router := mlsRouter(store, &mockMLSHub{})

	rr := putMLS(router, "/groups/"+channelID+"/info", token, map[string]interface{}{
		"groupInfo": "",
		"epoch":     int64(0),
	})
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestMLS_PutGroupInfo_NoAuth_Returns401(t *testing.T) {
	store := &mockStore{}
	channelID := uuid.New().String()
	router := mlsRouter(store, &mockMLSHub{})

	rr := putMLS(router, "/groups/"+channelID+"/info", "", map[string]interface{}{
		"groupInfo": base64.StdEncoding.EncodeToString([]byte("gi")),
		"epoch":     int64(1),
	})
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// ---------- POST /groups/:channelId/commit ----------

func TestMLS_PostCommit_Valid_Returns204(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	channelID := uuid.New().String()
	token := makeAuth(store, userID)
	hub := &mockMLSHub{}

	upsertCalled := false
	appendCalled := false
	store.upsertMLSGroupInfoFn = func(_ context.Context, _ string, _ string, _ []byte, _ int64) error {
		upsertCalled = true
		return nil
	}
	store.appendMLSCommitFn = func(_ context.Context, _ string, _ int64, _ []byte, _ string) error {
		appendCalled = true
		return nil
	}
	router := mlsRouter(store, hub)

	body := map[string]interface{}{
		"commitBytes": base64.StdEncoding.EncodeToString([]byte("commit-data")),
		"groupInfo":   base64.StdEncoding.EncodeToString([]byte("updated-group-info")),
		"epoch":       int64(5),
	}
	rr := postMLS(router, "/groups/"+channelID+"/commit", token, body)
	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.True(t, upsertCalled, "UpsertMLSGroupInfo must be called")
	assert.True(t, appendCalled, "AppendMLSCommit must be called")

	hub.mu.Lock()
	broadcastCount := len(hub.channelBroadcastCalls)
	hub.mu.Unlock()
	assert.Equal(t, 1, broadcastCount, "must broadcast mls.commit to channel")
}

// ---------- GET /groups/:channelId/commits ----------

func TestMLS_GetCommitsSinceEpoch_Valid_Returns200(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	channelID := uuid.New().String()
	token := makeAuth(store, userID)

	now := time.Now()
	store.getMLSCommitsSinceEpochFn = func(_ context.Context, cid string, sinceEpoch int64, limit int) ([]db.MLSCommitRow, error) {
		return []db.MLSCommitRow{
			{Epoch: 1, CommitBytes: []byte("c1"), SenderID: "user1", CreatedAt: now},
			{Epoch: 2, CommitBytes: []byte("c2"), SenderID: "user2", CreatedAt: now},
			{Epoch: 3, CommitBytes: []byte("c3"), SenderID: "user3", CreatedAt: now},
		}, nil
	}
	router := mlsRouter(store, &mockMLSHub{})

	rr := getMLS(router, "/groups/"+channelID+"/commits?since_epoch=0", token)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp struct {
		Commits []struct {
			Epoch       int64  `json:"epoch"`
			CommitBytes string `json:"commitBytes"`
			SenderID    string `json:"senderId"`
		} `json:"commits"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Len(t, resp.Commits, 3)
	assert.Equal(t, int64(1), resp.Commits[0].Epoch)
	assert.Equal(t, int64(3), resp.Commits[2].Epoch)
}

func TestMLS_GetCommitsSinceEpoch_Empty_Returns200WithEmptyArray(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	channelID := uuid.New().String()
	token := makeAuth(store, userID)

	store.getMLSCommitsSinceEpochFn = func(_ context.Context, _ string, _ int64, _ int) ([]db.MLSCommitRow, error) {
		return nil, nil
	}
	router := mlsRouter(store, &mockMLSHub{})

	rr := getMLS(router, "/groups/"+channelID+"/commits", token)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp struct {
		Commits []interface{} `json:"commits"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.NotNil(t, resp.Commits)
	assert.Empty(t, resp.Commits)
}

// ---------- GET /pending-welcomes ----------

func TestMLS_GetPendingWelcomes_HasWelcomes_Returns200(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	now := time.Now()
	store.getPendingWelcomesFn = func(_ context.Context, uid string) ([]db.PendingWelcomeRow, error) {
		return []db.PendingWelcomeRow{
			{ID: "w1", ChannelID: "ch1", WelcomeBytes: []byte("wb1"), SenderID: "s1", Epoch: 1, CreatedAt: now},
			{ID: "w2", ChannelID: "ch2", WelcomeBytes: []byte("wb2"), SenderID: "s2", Epoch: 2, CreatedAt: now},
		}, nil
	}
	router := mlsRouter(store, &mockMLSHub{})

	rr := getMLS(router, "/pending-welcomes", token)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp struct {
		Welcomes []struct {
			ID           string `json:"id"`
			ChannelID    string `json:"channelId"`
			WelcomeBytes string `json:"welcomeBytes"`
			SenderID     string `json:"senderId"`
			Epoch        int64  `json:"epoch"`
		} `json:"welcomes"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.Len(t, resp.Welcomes, 2)
	assert.Equal(t, "w1", resp.Welcomes[0].ID)
	assert.Equal(t, "ch2", resp.Welcomes[1].ChannelID)
}

func TestMLS_GetPendingWelcomes_Empty_Returns200WithEmptyArray(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	store.getPendingWelcomesFn = func(_ context.Context, _ string) ([]db.PendingWelcomeRow, error) {
		return nil, nil
	}
	router := mlsRouter(store, &mockMLSHub{})

	rr := getMLS(router, "/pending-welcomes", token)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp struct {
		Welcomes []interface{} `json:"welcomes"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.NotNil(t, resp.Welcomes)
	assert.Empty(t, resp.Welcomes)
}

// ---------- DELETE /pending-welcomes/:id ----------

func TestMLS_DeletePendingWelcome_Valid_Returns204(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)
	welcomeID := uuid.New().String()

	deleteCalled := false
	store.deletePendingWelcomeFn = func(_ context.Context, wid string) error {
		if wid == welcomeID {
			deleteCalled = true
		}
		return nil
	}
	router := mlsRouter(store, &mockMLSHub{})

	rr := deleteMLS(router, "/pending-welcomes/"+welcomeID, token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.True(t, deleteCalled, "DeletePendingWelcome must be called with the correct ID")
}

// ---------- Guild GroupInfo routes ----------

// mlsRouterWithGuildLevel wraps the MLS router to inject a guild permission level
// into the request context, simulating what RequireGuildMember does in production.
func mlsRouterWithGuildLevel(store *mockStore, level int) http.Handler {
	inner := mlsRouter(store, &mockMLSHub{})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := withGuildLevel(r.Context(), level)
		inner.ServeHTTP(w, r.WithContext(ctx))
	})
}

func TestGetGuildGroupInfo_NotFound_Returns404(t *testing.T) {
	store := &mockStore{
		getMLSGuildMetadataGroupInfoFn: func(_ context.Context, _ string) ([]byte, int64, error) {
			return nil, 0, nil // no record yet
		},
	}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	guildID := uuid.New().String()
	rr := getMLS(router, "/guilds/"+guildID+"/group-info", token)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestGetGuildGroupInfo_Found_Returns200WithBlob(t *testing.T) {
	guildID := uuid.New().String()
	expectedBlob := []byte("encrypted-guild-metadata")

	store := &mockStore{
		getMLSGuildMetadataGroupInfoFn: func(_ context.Context, gid string) ([]byte, int64, error) {
			if gid == guildID {
				return expectedBlob, 3, nil
			}
			return nil, 0, nil
		},
	}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	rr := getMLS(router, "/guilds/"+guildID+"/group-info", token)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp guildGroupInfoResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.NotEmpty(t, resp.GroupInfo)
	assert.Equal(t, int64(3), resp.Epoch)
}

func TestPutGuildGroupInfo_AdminLevel_Returns204(t *testing.T) {
	guildID := uuid.New().String()

	var storedBlob []byte
	var storedEpoch int64
	store := &mockStore{
		upsertMLSGuildMetadataGroupInfoFn: func(_ context.Context, gid string, blob []byte, epoch int64) error {
			storedBlob = blob
			storedEpoch = epoch
			return nil
		},
		getServerMemberLevelFn: func(_ context.Context, _, _ string) (int, error) {
			return models.PermissionLevelAdmin, nil
		},
	}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	encoded := base64.StdEncoding.EncodeToString([]byte("guild-blob"))
	rr := putMLS(router, "/guilds/"+guildID+"/group-info", token, map[string]interface{}{
		"groupInfo": encoded,
		"epoch":     int64(5),
	})
	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.Equal(t, []byte("guild-blob"), storedBlob)
	assert.Equal(t, int64(5), storedEpoch)
}

func TestPutGuildGroupInfo_MemberLevel_Returns204(t *testing.T) {
	guildID := uuid.New().String()
	store := &mockStore{
		upsertMLSGuildMetadataGroupInfoFn: func(_ context.Context, _ string, _ []byte, _ int64) error {
			return nil
		},
		getServerMemberLevelFn: func(_ context.Context, _, _ string) (int, error) {
			return models.PermissionLevelMember, nil
		},
	}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	encoded := base64.StdEncoding.EncodeToString([]byte("guild-blob"))
	rr := putMLS(router, "/guilds/"+guildID+"/group-info", token, map[string]interface{}{
		"groupInfo": encoded,
		"epoch":     int64(1),
	})
	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestPutGuildGroupInfo_NonMember_Returns403(t *testing.T) {
	guildID := uuid.New().String()
	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, _ string) (int, error) {
			return 0, fmt.Errorf("not a member")
		},
	}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	encoded := base64.StdEncoding.EncodeToString([]byte("guild-blob"))
	rr := putMLS(router, "/guilds/"+guildID+"/group-info", token, map[string]interface{}{
		"groupInfo": encoded,
		"epoch":     int64(1),
	})
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestDeleteGuildGroupInfo_AdminLevel_Returns204(t *testing.T) {
	guildID := uuid.New().String()

	deleteCalled := false
	store := &mockStore{
		deleteMLSGuildMetadataGroupInfoFn: func(_ context.Context, gid string) error {
			if gid == guildID {
				deleteCalled = true
			}
			return nil
		},
		getServerMemberLevelFn: func(_ context.Context, _, _ string) (int, error) {
			return models.PermissionLevelAdmin, nil
		},
	}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	rr := deleteMLS(router, "/guilds/"+guildID+"/group-info", token)
	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.True(t, deleteCalled, "DeleteMLSGuildMetadataGroupInfo must be called")
}

func TestDeleteGuildGroupInfo_MemberLevel_Returns403(t *testing.T) {
	guildID := uuid.New().String()
	store := &mockStore{
		getServerMemberLevelFn: func(_ context.Context, _, _ string) (int, error) {
			return models.PermissionLevelMember, nil
		},
	}
	token := makeAuth(store, uuid.New().String())
	router := mlsRouter(store, &mockMLSHub{})

	rr := deleteMLS(router, "/guilds/"+guildID+"/group-info", token)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

// ---------- Mock hub ----------

// mockMLSHub records BroadcastToUser and Broadcast calls for assertions.
type mockMLSHub struct {
	mu                    sync.Mutex
	broadcastCalls        []struct {
		userID  string
		message []byte
	}
	channelBroadcastCalls []struct {
		channelID string
		message   []byte
	}
}

func (m *mockMLSHub) BroadcastToUser(userID string, message []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.broadcastCalls = append(m.broadcastCalls, struct {
		userID  string
		message []byte
	}{userID, message})
}

func (m *mockMLSHub) Broadcast(channelID string, message []byte, excludeClientID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channelBroadcastCalls = append(m.channelBroadcastCalls, struct {
		channelID string
		message   []byte
	}{channelID, message})
}

