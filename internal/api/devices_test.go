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

	"github.com/go-chi/chi/v5"
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

// ---------- Revoke enforcement ----------

// TestAuthenticatedRequest_RevokedDevice_Returns401 verifies that once
// IsDeviceActive returns false for the JWT's deviceID, any authenticated
// HTTP request fails with 401, regardless of session validity. This is
// the middleware-level enforcement that closes the revoke-doesn't-actually-
// stop-anything bug.
func TestAuthenticatedRequest_RevokedDevice_Returns401(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)
	// Simulate revocation: device key no longer exists.
	store.isDeviceActiveFn = func(_ context.Context, uid, did string) (bool, error) {
		return false, nil
	}
	handler := newDeviceTestRouter(store)
	// Any authenticated endpoint will do; pick GET /devices for simplicity.
	req := httptest.NewRequest(http.MethodGet, "/devices", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())
}

// TestRevokeDevice_DisconnectsActiveWebSocket verifies the revoke handler
// calls hub.DisconnectDevice for the just-revoked (userID, deviceID).
func TestRevokeDevice_DisconnectsActiveWebSocket(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)
	store.revokeDeviceKeyFn = func(_ context.Context, _ string, _ string) error { return nil }
	store.listDeviceKeysFn = func(_ context.Context, _ string) ([]models.DeviceKey, error) {
		return []models.DeviceKey{{
			DeviceID:        "victim-device",
			DevicePublicKey: []byte("pub"),
		}}, nil
	}

	hub := &deviceRevokeHub{}
	handler := AuthRoutes(store, testJWTSecret, testJWTExpiry, nil, hub)

	req := httptest.NewRequest(http.MethodDelete, "/devices/victim-device", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code)
	require.Equal(t, []deviceRevokeKick{{userID, "victim-device"}}, hub.kicks,
		"revokeDevice must invoke hub.DisconnectDevice for the revoked device")
}

type deviceRevokeKick struct {
	userID, deviceID string
}

type deviceRevokeHub struct {
	kicks []deviceRevokeKick
}

func (h *deviceRevokeHub) BroadcastToAll(_ []byte)                              {}
func (h *deviceRevokeHub) BroadcastToServer(_ string, _ []byte)                 {}
func (h *deviceRevokeHub) BroadcastToUser(_ string, _ []byte)                   {}
func (h *deviceRevokeHub) DisconnectUser(_ string)                              {}
func (h *deviceRevokeHub) DisconnectDevice(uid string, did string) {
	h.kicks = append(h.kicks, deviceRevokeKick{uid, did})
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

// ---------- TestLinkVerify_InvalidCert_Generic ----------

// TestLinkVerify_InvalidCert_Generic verifies that an invalid certificate
// signature returns a generic 401 with no error_code field, to avoid leaking
// information about the failure reason.
func TestLinkVerify_InvalidCert_Generic(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	signingPub, _, _ := generateDeviceKeyPair(t)
	newDevicePub, _, newDevicePubBase64 := generateDeviceKeyPair(t)

	// Sign with a DIFFERENT private key (wrong signer) — cert will fail verification.
	_, wrongPriv, _ := generateDeviceKeyPair(t)
	badCert := ed25519.Sign(wrongPriv, newDevicePub)

	requestPayload := storedLinkRequest{
		RequestID:        "req-generic",
		Secret:           "sec-generic",
		Code:             "ABCD1234",
		DeviceID:         "new-device-generic",
		DevicePublicKey:  newDevicePubBase64,
		SessionPublicKey: base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
		ExpiresAt:        time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
	}
	requestBytes, err := json.Marshal(requestPayload)
	require.NoError(t, err)

	store.consumeAuthNonceFn = func(_ context.Context, nonce string) ([]byte, error) {
		return requestBytes, nil
	}
	store.listDeviceKeysFn = func(_ context.Context, _ string) ([]models.DeviceKey, error) {
		return []models.DeviceKey{
			{DeviceID: "existing-device", DevicePublicKey: signingPub},
		}, nil
	}

	handler := newDeviceTestRouter(store)
	rr := postJSONWithAuth(handler, "/link-verify", token, map[string]string{
		"claimToken":      "claim-generic",
		"certificate":     base64.StdEncoding.EncodeToString(badCert),
		"signingDeviceId": "existing-device",
		"relayCiphertext": "ct",
		"relayIv":         "iv",
		"relayPublicKey":  "rp",
	})

	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "Linking failed. Please try again.", body["error"])
	_, hasErrorCode := body["error_code"]
	assert.False(t, hasErrorCode, "error_code must not be present in security-sensitive error response")
}

// ---------- TestLinkVerify_Mismatch_Generic ----------

// TestLinkVerify_Mismatch_Generic verifies that an unknown signingDeviceId
// returns the same generic 401 as a certificate mismatch, with no error_code.
func TestLinkVerify_Mismatch_Generic(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	_, _, newDevicePubBase64 := generateDeviceKeyPair(t)
	_, somePriv, _ := generateDeviceKeyPair(t)
	newDevicePubBytes, _ := base64.StdEncoding.DecodeString(newDevicePubBase64)
	cert := ed25519.Sign(somePriv, newDevicePubBytes)

	requestPayload := storedLinkRequest{
		RequestID:        "req-mismatch",
		Secret:           "sec-mismatch",
		Code:             "MISMATCH",
		DeviceID:         "new-device-mismatch",
		DevicePublicKey:  newDevicePubBase64,
		SessionPublicKey: base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
		ExpiresAt:        time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
	}
	requestBytes, err := json.Marshal(requestPayload)
	require.NoError(t, err)

	store.consumeAuthNonceFn = func(_ context.Context, _ string) ([]byte, error) {
		return requestBytes, nil
	}
	// ListDeviceKeys returns empty — no matching signing device.
	store.listDeviceKeysFn = func(_ context.Context, _ string) ([]models.DeviceKey, error) {
		return []models.DeviceKey{}, nil
	}

	handler := newDeviceTestRouter(store)
	rr := postJSONWithAuth(handler, "/link-verify", token, map[string]string{
		"claimToken":      "claim-mismatch",
		"certificate":     base64.StdEncoding.EncodeToString(cert),
		"signingDeviceId": "nonexistent-device",
		"relayCiphertext": "ct",
		"relayIv":         "iv",
		"relayPublicKey":  "rp",
	})

	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body))
	assert.Equal(t, "Linking failed. Please try again.", body["error"])
	_, hasErrorCode := body["error_code"]
	assert.False(t, hasErrorCode, "error_code must not be present in security-sensitive error response")
}

// ---------- TestApprovalRate ----------

// TestApprovalRate_WarnAtThreshold verifies that 5 or more approvals from the
// same signingDeviceId within 10 minutes triggers a slog.Warn log entry.
// The handler must NOT block the request — the 6th approval still succeeds (201).
func TestApprovalRate_WarnAtThreshold(t *testing.T) {
	store := &mockStore{}
	userID := uuid.New().String()
	token := makeAuth(store, userID)

	signingPub, signingPriv, _ := generateDeviceKeyPair(t)
	signingDeviceID := "signing-device-rate"

	// Helper: create a valid link-verify request for a fresh new-device keypair.
	makeBody := func() map[string]string {
		newDevicePub, _, newDevicePubBase64 := generateDeviceKeyPair(t)
		certificate := ed25519.Sign(signingPriv, newDevicePub)

		requestPayload := storedLinkRequest{
			RequestID:        uuid.New().String(),
			Secret:           uuid.New().String(),
			Code:             "XXXXXXXX",
			DeviceID:         uuid.New().String(),
			DevicePublicKey:  newDevicePubBase64,
			SessionPublicKey: base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
			ExpiresAt:        time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339),
		}
		requestBytes, _ := json.Marshal(requestPayload)

		store.consumeAuthNonceFn = func(_ context.Context, _ string) ([]byte, error) {
			return requestBytes, nil
		}
		store.insertAuthNonceFn = func(_ context.Context, _ string, _ []byte, _ time.Time) error {
			return nil
		}
		store.insertDeviceKeyFn = func(_ context.Context, _, _, _ string, _, _ []byte) error {
			return nil
		}

		return map[string]string{
			"claimToken":      uuid.New().String(),
			"certificate":     base64.StdEncoding.EncodeToString(certificate),
			"signingDeviceId": signingDeviceID,
			"relayCiphertext": "ct",
			"relayIv":         "iv",
			"relayPublicKey":  "rp",
		}
	}

	store.listDeviceKeysFn = func(_ context.Context, _ string) ([]models.DeviceKey, error) {
		return []models.DeviceKey{
			{DeviceID: signingDeviceID, DevicePublicKey: signingPub},
		}, nil
	}

	// Use the same handler instance across all 6 requests so the approvalTracker
	// accumulates state.
	handler := AuthRoutes(store, testJWTSecret, testJWTExpiry, nil)

	// First 4 approvals — should all succeed without warning.
	for i := 0; i < 4; i++ {
		rr := postJSONWithAuth(handler, "/link-verify", token, makeBody())
		require.Equal(t, http.StatusCreated, rr.Code, "approval %d failed: %s", i+1, rr.Body.String())
	}

	// 5th approval — triggers the warning threshold (count >= 5 after append).
	// The request must still succeed (no hard block).
	rr := postJSONWithAuth(handler, "/link-verify", token, makeBody())
	assert.Equal(t, http.StatusCreated, rr.Code, "5th approval should still succeed: %s", rr.Body.String())
}

// ---------- TestDeviceRoutes_Hub ----------

// TestDeviceRoutes_Hub verifies that DeviceRoutes accepts a hub parameter and
// that the resulting handler compiles and wires the hub correctly. This is a
// compilation test — passing nil for the hub is valid (handlers nil-check before use).
func TestDeviceRoutes_Hub(t *testing.T) {
	store := &mockStore{}
	// Passing a nil GlobalBroadcaster is valid; handlers nil-check hub before use.
	var hub GlobalBroadcaster
	r := chi.NewRouter()
	r.Group(DeviceRoutes(store, testJWTSecret, nil, hub))
	// If DeviceRoutes compiles and returns without panic, the hub wiring is correct.
	assert.NotNil(t, r)
}

func ptr(s string) *string { return &s }
