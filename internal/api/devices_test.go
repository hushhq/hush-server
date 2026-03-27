package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"hush.app/server/internal/models"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newDeviceTestRouter returns the shared AuthRoutes handler (which mounts
// /devices and /link-* sub-routes) for device handler tests.
func newDeviceTestRouter(store *mockStore) http.Handler {
	return AuthRoutes(store, testJWTSecret, testJWTExpiry, nil)
}

// deleteWithAuthReq performs an authenticated DELETE request.
func deleteWithAuthReq(handler http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// postJSONWithAuth performs an authenticated POST with a JSON body.
func postJSONWithAuth(handler http.Handler, path, token string, body interface{}) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

// generateDeviceKeyPair generates an Ed25519 keypair for a device and returns
// the raw public key (32 bytes), raw private key, and base64-encoded public key.
func generateDeviceKeyPair(t *testing.T) (pubRaw []byte, priv ed25519.PrivateKey, pubBase64 string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, priv, base64.StdEncoding.EncodeToString(pub)
}

// ---------- TestListDevices ----------

func TestListDevices_ReturnsDeviceList(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	certifiedAt := time.Now().UTC().Truncate(time.Second)
	lastSeen := certifiedAt.Add(time.Hour)

	store.listDeviceKeysFn = func(_ context.Context, uid string) ([]models.DeviceKey, error) {
		require.Equal(t, userID, uid)
		return []models.DeviceKey{
			{
				ID:          uuid.New().String(),
				UserID:      uid,
				DeviceID:    "device-1",
				CertifiedAt: certifiedAt,
				LastSeen:    &lastSeen,
				Label:       ptr("iPhone"),
			},
		}, nil
	}

	handler := newDeviceTestRouter(store)
	rr := getWithAuth(handler, "/devices", token)
	require.Equal(t, http.StatusOK, rr.Code)

	var result []map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&result))
	require.Len(t, result, 1)
	assert.Equal(t, "device-1", result[0]["deviceId"])
	assert.Equal(t, "iPhone", result[0]["label"])
	// Raw keys must never appear in the list response.
	_, hasDevicePubKey := result[0]["devicePublicKey"]
	assert.False(t, hasDevicePubKey, "devicePublicKey must not be exposed in list response")
	_, hasCert := result[0]["certificate"]
	assert.False(t, hasCert, "certificate must not be exposed in list response")
}

func TestListDevices_Unauthenticated_Returns401(t *testing.T) {
	store := &mockStore{}
	handler := newDeviceTestRouter(store)
	req := httptest.NewRequest(http.MethodGet, "/devices", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// ---------- TestCertifyDevice ----------

func TestCertifyDevice_Valid_Returns201(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()

	// Existing (signing) device keypair — registered and stored in device_keys.
	signingPub, signingPriv, _ := generateDeviceKeyPair(t)

	// New device keypair — to be certified.
	newDevicePub, _, newDevicePubBase64 := generateDeviceKeyPair(t)

	// Sign the new device's public key with the existing device's private key.
	certificate := ed25519.Sign(signingPriv, newDevicePub)
	certBase64 := base64.StdEncoding.EncodeToString(certificate)

	signingDeviceID := "existing-device"

	// Stub ListDeviceKeys to return the signing device so the handler can retrieve its public key.
	store.listDeviceKeysFn = func(_ context.Context, uid string) ([]models.DeviceKey, error) {
		return []models.DeviceKey{
			{DeviceID: signingDeviceID, DevicePublicKey: signingPub},
		}, nil
	}

	var insertedUserID, insertedDeviceID string
	var insertedPubKey, insertedCert []byte
	store.insertDeviceKeyFn = func(_ context.Context, uid, did string, pub, cert []byte) error {
		insertedUserID = uid
		insertedDeviceID = did
		insertedPubKey = pub
		insertedCert = cert
		return nil
	}

	token := makeAuth(store, userID)
	handler := newDeviceTestRouter(store)

	body := map[string]string{
		"devicePublicKey": newDevicePubBase64,
		"certificate":     certBase64,
		"deviceId":        "new-device-abc",
		"label":           "Laptop",
		"signingDeviceId": signingDeviceID,
	}
	rr := postJSONWithAuth(handler, "/devices", token, body)
	require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())

	assert.Equal(t, userID, insertedUserID)
	assert.Equal(t, "new-device-abc", insertedDeviceID)
	assert.Equal(t, newDevicePub, insertedPubKey)
	assert.Equal(t, certificate, insertedCert)
}

func TestCertifyDevice_InvalidCert_Returns401(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()

	signingPub, _, _ := generateDeviceKeyPair(t)
	_, _, newDevicePubBase64 := generateDeviceKeyPair(t)

	// Certificate signed by a DIFFERENT (unknown) key — should fail verification.
	_, unknownPriv, _ := generateDeviceKeyPair(t)
	newDevicePubBytes, err := base64.StdEncoding.DecodeString(newDevicePubBase64)
	require.NoError(t, err)
	badCert := ed25519.Sign(unknownPriv, newDevicePubBytes)

	signingDeviceID := "existing-device"
	store.listDeviceKeysFn = func(_ context.Context, uid string) ([]models.DeviceKey, error) {
		return []models.DeviceKey{
			{DeviceID: signingDeviceID, DevicePublicKey: signingPub},
		}, nil
	}

	token := makeAuth(store, userID)
	handler := newDeviceTestRouter(store)

	body := map[string]string{
		"devicePublicKey": newDevicePubBase64,
		"certificate":     base64.StdEncoding.EncodeToString(badCert),
		"deviceId":        "new-device-bad",
		"signingDeviceId": signingDeviceID,
	}
	rr := postJSONWithAuth(handler, "/devices", token, body)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestCertifyDevice_MissingSigningDevice_Returns400(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()

	_, _, newDevicePubBase64 := generateDeviceKeyPair(t)
	_, fakeCertPriv, _ := generateDeviceKeyPair(t)
	newDevicePubBytes, _ := base64.StdEncoding.DecodeString(newDevicePubBase64)
	cert := ed25519.Sign(fakeCertPriv, newDevicePubBytes)

	// No signing device in the store — ListDeviceKeys returns empty list.
	store.listDeviceKeysFn = func(_ context.Context, uid string) ([]models.DeviceKey, error) {
		return []models.DeviceKey{}, nil
	}

	token := makeAuth(store, userID)
	handler := newDeviceTestRouter(store)

	body := map[string]string{
		"devicePublicKey": newDevicePubBase64,
		"certificate":     base64.StdEncoding.EncodeToString(cert),
		"deviceId":        "new-device",
		"signingDeviceId": "nonexistent-device",
	}
	rr := postJSONWithAuth(handler, "/devices", token, body)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestCertifyDevice_InvalidPublicKeySize_Returns400(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)
	handler := newDeviceTestRouter(store)

	body := map[string]string{
		"devicePublicKey": base64.StdEncoding.EncodeToString([]byte("short")),
		"certificate":     base64.StdEncoding.EncodeToString(make([]byte, 64)),
		"deviceId":        "new-device",
		"signingDeviceId": "existing-device",
	}
	rr := postJSONWithAuth(handler, "/devices", token, body)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------- TestRevokeDevice ----------

func TestRevokeDevice_Valid_Returns204(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	var revokedUserID, revokedDeviceID string
	store.revokeDeviceKeyFn = func(_ context.Context, uid, did string) error {
		revokedUserID = uid
		revokedDeviceID = did
		return nil
	}

	handler := newDeviceTestRouter(store)
	req := httptest.NewRequest(http.MethodDelete, "/devices/my-device-id", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.Equal(t, userID, revokedUserID)
	assert.Equal(t, "my-device-id", revokedDeviceID)
}

func TestRevokeDevice_Unauthenticated_Returns401(t *testing.T) {
	store := &mockStore{}
	handler := newDeviceTestRouter(store)
	req := httptest.NewRequest(http.MethodDelete, "/devices/some-device", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// ---------- TestRevokeAllDevices ----------

func TestRevokeAllDevices_Valid_Returns204(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	var revokedAll bool
	store.revokeAllDeviceKeysFn = func(_ context.Context, uid string) error {
		require.Equal(t, userID, uid)
		revokedAll = true
		return nil
	}

	handler := newDeviceTestRouter(store)
	req := httptest.NewRequest(http.MethodDelete, "/devices?all=true", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
	assert.True(t, revokedAll)
}

// ---------- TestLinkRequest / TestLinkVerify ----------

func TestLinkRequest_ReturnsCodeAndExpiry(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	// InsertAuthNonce is used to persist the link-request nonce.
	store.insertAuthNonceFn = func(_ context.Context, nonce string, pub []byte, expiresAt time.Time) error {
		// Nonce should be the 8-char code with "link:" prefix.
		assert.Contains(t, nonce, "link:")
		assert.WithinDuration(t, time.Now().Add(5*time.Minute), expiresAt, 10*time.Second)
		return nil
	}

	body := map[string]string{
		"devicePublicKey": base64.StdEncoding.EncodeToString(make([]byte, 32)),
	}
	handler := newDeviceTestRouter(store)
	rr := postJSONWithAuth(handler, "/link-request", token, body)
	require.Equal(t, http.StatusOK, rr.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	code, ok := resp["code"].(string)
	require.True(t, ok, "response must have a 'code' string field")
	assert.Len(t, code, 8, "linking code must be 8 characters")
	_, hasExpiry := resp["expiresAt"]
	assert.True(t, hasExpiry, "response must include 'expiresAt'")
}

func TestLinkVerify_ValidCode_Returns201(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	// Signing device's keypair.
	signingPub, signingPriv, _ := generateDeviceKeyPair(t)
	// New device's public key.
	newDevicePub, _, newDevicePubBase64 := generateDeviceKeyPair(t)

	// Certificate = Sign(signingPriv, newDevicePub).
	certificate := ed25519.Sign(signingPriv, newDevicePub)

	code := "ABCD1234"
	nonce := fmt.Sprintf("link:%s", code)

	// consumeAuthNonce returns the new device's public key encoded in the nonce.
	store.consumeAuthNonceFn = func(_ context.Context, n string) ([]byte, error) {
		require.Equal(t, nonce, n)
		return newDevicePub, nil
	}

	// The signing device's public key is looked up via ListDeviceKeys.
	store.listDeviceKeysFn = func(_ context.Context, uid string) ([]models.DeviceKey, error) {
		return []models.DeviceKey{
			{DeviceID: "existing-device", DevicePublicKey: signingPub},
		}, nil
	}

	var insertedDeviceID string
	store.insertDeviceKeyFn = func(_ context.Context, uid, did string, pub, cert []byte) error {
		insertedDeviceID = did
		return nil
	}

	handler := newDeviceTestRouter(store)
	body := map[string]string{
		"code":            code,
		"certificate":     base64.StdEncoding.EncodeToString(certificate),
		"newDeviceId":     "new-device-via-code",
		"signingDeviceId": "existing-device",
		"devicePublicKey": newDevicePubBase64,
	}
	rr := postJSONWithAuth(handler, "/link-verify", token, body)
	require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())
	assert.Equal(t, "new-device-via-code", insertedDeviceID)
}

func ptr(s string) *string { return &s }
