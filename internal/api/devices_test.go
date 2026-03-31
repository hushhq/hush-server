package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hushhq/hush-server/internal/models"

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

func postJSONReq(handler http.Handler, path string, body interface{}) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
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

	// Existing (signing) device keypair - registered and stored in device_keys.
	signingPub, signingPriv, _ := generateDeviceKeyPair(t)

	// New device keypair - to be certified.
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
	var insertedLabel string
	var insertedPubKey, insertedCert []byte
	store.insertDeviceKeyFn = func(_ context.Context, uid, did, label string, pub, cert []byte) error {
		insertedUserID = uid
		insertedDeviceID = did
		insertedLabel = label
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
	assert.Equal(t, "Laptop", insertedLabel)
	assert.Equal(t, newDevicePub, insertedPubKey)
	assert.Equal(t, certificate, insertedCert)
}

func TestCertifyDevice_InvalidCert_Returns401(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()

	signingPub, _, _ := generateDeviceKeyPair(t)
	_, _, newDevicePubBase64 := generateDeviceKeyPair(t)

	// Certificate signed by a DIFFERENT (unknown) key - should fail verification.
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

	// No signing device in the store - ListDeviceKeys returns empty list.
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

// ---------- TestLinkRequest / TestLinkResolve / TestLinkVerify ----------

func TestLinkRequest_ReturnsHandleCodeAndExpiry(t *testing.T) {
	store := &mockStore{}
	insertedNonces := make([]string, 0, 2)

	store.insertAuthNonceFn = func(_ context.Context, nonce string, payload []byte, expiresAt time.Time) error {
		insertedNonces = append(insertedNonces, nonce)
		assert.WithinDuration(t, time.Now().Add(5*time.Minute), expiresAt, 10*time.Second)
		assert.NotEmpty(t, payload)
		return nil
	}

	body := map[string]string{
		"devicePublicKey":  base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"sessionPublicKey": base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
		"deviceId":         "new-device-1",
		"instanceUrl":      "https://app.gethush.live",
	}
	handler := newDeviceTestRouter(store)
	rr := postJSONReq(handler, "/link-request", body)
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	var resp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.NotEmpty(t, resp["requestId"])
	require.NotEmpty(t, resp["secret"])
	assert.Len(t, resp["code"], 8)
	require.NotEmpty(t, resp["expiresAt"])
	require.Len(t, insertedNonces, 2)
	assert.Contains(t, insertedNonces[0], linkRequestNoncePrefix)
	assert.Contains(t, insertedNonces[1], linkCodeNoncePrefix)
}

func TestLinkResolve_ValidCode_ReturnsClaimToken(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	payload := storedLinkRequest{
		RequestID:        "req-1",
		Secret:           "sec-1",
		Code:             "ABCD1234",
		DeviceID:         "new-device-1",
		DevicePublicKey:  base64.StdEncoding.EncodeToString(make([]byte, 32)),
		SessionPublicKey: base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
		InstanceURL:      "https://app.gethush.live",
		ExpiresAt:        time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
	}
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	store.consumeAuthNonceFn = func(_ context.Context, nonce string) ([]byte, error) {
		require.Equal(t, linkCodeNonce("ABCD1234"), nonce)
		return payloadBytes, nil
	}

	var deletedNonce string
	store.deleteAuthNonceFn = func(_ context.Context, nonce string) error {
		deletedNonce = nonce
		return nil
	}

	var claimNonce string
	store.insertAuthNonceFn = func(_ context.Context, nonce string, body []byte, _ time.Time) error {
		claimNonce = nonce
		assert.Equal(t, payloadBytes, body)
		return nil
	}

	handler := newDeviceTestRouter(store)
	rr := postJSONWithAuth(handler, "/link-resolve", token, map[string]string{
		"code": "ABCD1234",
	})
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	var resp map[string]string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	require.NotEmpty(t, resp["claimToken"])
	assert.Equal(t, payload.RequestID, resp["requestId"])
	assert.Equal(t, payload.DeviceID, resp["deviceId"])
	assert.Equal(t, linkRequestNonce(payload.RequestID, payload.Secret), deletedNonce)
	assert.Contains(t, claimNonce, linkClaimNoncePrefix)
}

func TestLinkVerify_ValidClaim_Returns201(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	signingPub, signingPriv, _ := generateDeviceKeyPair(t)
	newDevicePub, _, newDevicePubBase64 := generateDeviceKeyPair(t)
	certificate := ed25519.Sign(signingPriv, newDevicePub)

	requestPayload := storedLinkRequest{
		RequestID:        "req-1",
		Secret:           "sec-1",
		Code:             "ABCD1234",
		DeviceID:         "new-device-via-link",
		DevicePublicKey:  newDevicePubBase64,
		SessionPublicKey: base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
		Label:            "Safari on iPhone",
		InstanceURL:      "https://app.gethush.live",
		ExpiresAt:        time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
	}
	requestBytes, err := json.Marshal(requestPayload)
	require.NoError(t, err)

	store.consumeAuthNonceFn = func(_ context.Context, nonce string) ([]byte, error) {
		require.Equal(t, linkClaimNonce("claim-1"), nonce)
		return requestBytes, nil
	}

	store.listDeviceKeysFn = func(_ context.Context, uid string) ([]models.DeviceKey, error) {
		require.Equal(t, userID, uid)
		return []models.DeviceKey{
			{DeviceID: "existing-device", DevicePublicKey: signingPub},
		}, nil
	}

	var insertedDeviceID string
	var insertedLabel string
	store.insertDeviceKeyFn = func(_ context.Context, uid, did, label string, pub, cert []byte) error {
		require.Equal(t, userID, uid)
		insertedDeviceID = did
		insertedLabel = label
		assert.Equal(t, newDevicePub, pub)
		assert.Equal(t, certificate, cert)
		return nil
	}

	var resultNonce string
	store.insertAuthNonceFn = func(_ context.Context, nonce string, payload []byte, _ time.Time) error {
		resultNonce = nonce
		assert.NotEmpty(t, payload)
		return nil
	}

	handler := newDeviceTestRouter(store)
	rr := postJSONWithAuth(handler, "/link-verify", token, map[string]string{
		"claimToken":      "claim-1",
		"certificate":     base64.StdEncoding.EncodeToString(certificate),
		"signingDeviceId": "existing-device",
		"relayCiphertext": "ciphertext",
		"relayIv":         "iv",
		"relayPublicKey":  "relay-pub",
	})
	require.Equal(t, http.StatusCreated, rr.Code, "body: %s", rr.Body.String())
	assert.Equal(t, requestPayload.DeviceID, insertedDeviceID)
	assert.Equal(t, requestPayload.Label, insertedLabel)
	assert.Equal(t, linkResultNonce(requestPayload.RequestID, requestPayload.Secret), resultNonce)
}

func TestLinkResult_WhenReady_ReturnsRelayPayload(t *testing.T) {
	store := &mockStore{}
	result := storedLinkResult{
		RelayCiphertext: "ciphertext",
		RelayIV:         "iv",
		RelayPublicKey:  "relay-pub",
		DeviceID:        "new-device-1",
		InstanceURL:     "https://app.gethush.live",
	}
	resultBytes, err := json.Marshal(result)
	require.NoError(t, err)

	store.consumeAuthNonceFn = func(_ context.Context, nonce string) ([]byte, error) {
		require.Equal(t, linkResultNonce("req-1", "sec-1"), nonce)
		return resultBytes, nil
	}

	handler := newDeviceTestRouter(store)
	rr := postJSONReq(handler, "/link-result", map[string]string{
		"requestId": "req-1",
		"secret":    "sec-1",
	})
	require.Equal(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	var resp storedLinkResult
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, result, resp)
}

func ptr(s string) *string { return &s }
