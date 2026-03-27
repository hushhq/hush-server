package api

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"hush.app/server/internal/db"
	"hush.app/server/internal/transparency"

	"github.com/go-chi/chi/v5"
)

const (
	linkCodeLen       = 8
	linkCodeExpiry    = 5 * time.Minute
	linkNoncePrefix   = "link:"
	ed25519CertLen    = 64
)

var linkCodeCharset = []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

// DeviceRoutes returns the chi.Router for device management endpoints.
// All routes require the caller to be authenticated (RequireAuth middleware
// is applied inside the router group). The routes are intended to be mounted
// at /api/auth/devices (and /api/auth/link-*).
//
// Route map:
//
//	GET    /devices           — list authenticated user's devices
//	POST   /devices           — certify a new device (QR flow)
//	DELETE /devices/{deviceId} — revoke a single device
//	DELETE /devices?all=true  — revoke all devices
//	POST   /link-request      — register a new device's public key; returns 8-char code
//	POST   /link-verify       — verify code + certificate; inserts certified device
//
// transparencySvc may be nil when the transparency log is not configured for
// this instance. Device certify and revoke operations append synchronously
// when the service is non-nil (non-fatal on failure).
func DeviceRoutes(store db.Store, jwtSecret string, transparencySvc *transparency.TransparencyService) func(r chi.Router) {
	h := &deviceHandler{store: store, transparencySvc: transparencySvc}
	return func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(RequireAuth(jwtSecret, store))

			// Device key management.
			r.Route("/devices", func(r chi.Router) {
				r.Get("/", h.listDevices)
				r.Post("/", h.certifyDevice)
				r.Delete("/", h.revokeAllDevices)
				r.Delete("/{deviceId}", h.revokeDevice)
			})

			// Text-code linking flow.
			r.Post("/link-request", h.linkRequest)
			r.Post("/link-verify", h.linkVerify)
		})
	}
}

type deviceHandler struct {
	store           db.Store
	transparencySvc *transparency.TransparencyService
}

// ---------- List ----------

// listDevices handles GET /api/auth/devices.
// Returns all device keys for the authenticated user.
// Raw public keys and certificates are never included in the response.
func (h *deviceHandler) listDevices(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	keys, err := h.store.ListDeviceKeys(r.Context(), userID)
	if err != nil {
		slog.Error("list device keys", "err", err, "user_id", userID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not list devices"})
		return
	}

	type deviceView struct {
		ID          string     `json:"id"`
		DeviceID    string     `json:"deviceId"`
		Label       *string    `json:"label,omitempty"`
		CertifiedAt time.Time  `json:"certifiedAt"`
		LastSeen    *time.Time `json:"lastSeen,omitempty"`
	}

	views := make([]deviceView, 0, len(keys))
	for _, k := range keys {
		views = append(views, deviceView{
			ID:          k.ID,
			DeviceID:    k.DeviceID,
			Label:       k.Label,
			CertifiedAt: k.CertifiedAt,
			LastSeen:    k.LastSeen,
		})
	}

	writeJSON(w, http.StatusOK, views)
}

// ---------- Certify ----------

// certifyDevice handles POST /api/auth/devices.
//
// Request body:
//
//	{
//	  "devicePublicKey": "<base64 32-byte Ed25519 public key>",
//	  "certificate":     "<base64 64-byte Ed25519 signature>",
//	  "deviceId":        "<string — caller-assigned device ID>",
//	  "label":           "<string — optional human-readable name>",
//	  "signingDeviceId": "<string — device ID of the authorising device>"
//	}
//
// The certificate must be a valid Ed25519 signature of devicePublicKey made by
// the private key corresponding to the signing device's stored public key.
func (h *deviceHandler) certifyDevice(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	var req struct {
		DevicePublicKey string `json:"devicePublicKey"`
		Certificate     string `json:"certificate"`
		DeviceID        string `json:"deviceId"`
		Label           string `json:"label"`
		SigningDeviceID  string `json:"signingDeviceId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	newDevicePubBytes, err := base64.StdEncoding.DecodeString(req.DevicePublicKey)
	if err != nil || len(newDevicePubBytes) != ed25519PublicKeyLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "devicePublicKey must be 32 bytes (Ed25519)"})
		return
	}

	certBytes, err := base64.StdEncoding.DecodeString(req.Certificate)
	if err != nil || len(certBytes) != ed25519CertLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "certificate must be 64 bytes (Ed25519 signature)"})
		return
	}

	if req.DeviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "deviceId is required"})
		return
	}

	if req.SigningDeviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "signingDeviceId is required"})
		return
	}

	signingPub, err := h.findDevicePublicKey(r.Context(), userID, req.SigningDeviceID)
	if err != nil {
		slog.Error("find signing device public key", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not look up signing device"})
		return
	}
	if signingPub == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "signing device not found"})
		return
	}

	if !ed25519.Verify(signingPub, newDevicePubBytes, certBytes) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid certificate"})
		return
	}

	if err := h.store.InsertDeviceKey(r.Context(), userID, req.DeviceID, newDevicePubBytes, certBytes); err != nil {
		slog.Error("insert device key", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not register device"})
		return
	}

	// Append transparency log entry synchronously. The user's root key is the
	// signing device's public key; the subject key is the newly certified device.
	if h.transparencySvc != nil {
		entry := &transparency.LogEntry{
			OperationType: transparency.OpDeviceAdd,
			UserPublicKey: signingPub,
			SubjectKey:    newDevicePubBytes,
			Timestamp:     time.Now().Unix(),
		}
		if logErr := h.transparencySvc.AppendAndNotify(r.Context(), entry, userID); logErr != nil {
			slog.Error("transparency: append device_add entry", "err", logErr, "user_id", userID)
		}
	}

	w.WriteHeader(http.StatusCreated)
}

// ---------- Revoke single ----------

// revokeDevice handles DELETE /api/auth/devices/{deviceId}.
func (h *deviceHandler) revokeDevice(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	deviceID := chi.URLParam(r, "deviceId")
	if deviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "deviceId is required"})
		return
	}

	// Look up the device's public key before revocation so we can log the subject.
	revokedPub, _ := h.findDevicePublicKey(r.Context(), userID, deviceID)

	if err := h.store.RevokeDeviceKey(r.Context(), userID, deviceID); err != nil {
		slog.Error("revoke device key", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not revoke device"})
		return
	}

	// Append transparency log entry synchronously.
	// UserPublicKey is the user's root key (obtained from auth context if available).
	// We use revokedPub as SubjectKey so verifiers can identify which device was revoked.
	if h.transparencySvc != nil && revokedPub != nil {
		entry := &transparency.LogEntry{
			OperationType: transparency.OpDeviceRevoke,
			SubjectKey:    revokedPub,
			Timestamp:     time.Now().Unix(),
		}
		if logErr := h.transparencySvc.AppendAndNotify(r.Context(), entry, userID); logErr != nil {
			slog.Error("transparency: append device_revoke entry", "err", logErr, "user_id", userID)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---------- Revoke all ----------

// revokeAllDevices handles DELETE /api/auth/devices?all=true.
// Used during the account recovery flow to wipe all device keys.
func (h *deviceHandler) revokeAllDevices(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("all") != "true" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "use ?all=true to revoke all devices"})
		return
	}

	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	if err := h.store.RevokeAllDeviceKeys(r.Context(), userID); err != nil {
		slog.Error("revoke all device keys", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not revoke all devices"})
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ---------- Link request (text-code flow) ----------

// linkRequest handles POST /api/auth/link-request.
//
// The new device POSTs its own public key. The server:
//  1. Generates an 8-character alphanumeric code.
//  2. Stores the code as an auth_nonce (prefix "link:") with the new device's
//     public key as the associated payload and a 5-minute expiry.
//  3. Returns { code, expiresAt } to the new device.
//
// The existing device then enters this code and calls /link-verify.
func (h *deviceHandler) linkRequest(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	var req struct {
		DevicePublicKey string `json:"devicePublicKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	newDevicePubBytes, err := base64.StdEncoding.DecodeString(req.DevicePublicKey)
	if err != nil || len(newDevicePubBytes) != ed25519PublicKeyLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "devicePublicKey must be 32 bytes (Ed25519)"})
		return
	}

	code := generateLinkCode()
	nonce := linkNoncePrefix + code
	expiresAt := time.Now().Add(linkCodeExpiry)

	if err := h.store.InsertAuthNonce(r.Context(), nonce, newDevicePubBytes, expiresAt); err != nil {
		slog.Error("insert link request nonce", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create link request"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"code":      code,
		"expiresAt": expiresAt.UTC().Format(time.RFC3339),
	})
}

// ---------- Link verify (text-code flow) ----------

// linkVerify handles POST /api/auth/link-verify.
//
// The existing device POSTs:
//
//	{
//	  "code":            "<8-char code from /link-request>",
//	  "certificate":     "<base64 64-byte signature of newDevicePub>",
//	  "newDeviceId":     "<device ID for the new device>",
//	  "signingDeviceId": "<device ID of the authorising device>",
//	  "devicePublicKey": "<base64 newDevicePub (for cross-check)>"
//	}
//
// The server:
//  1. Consumes the nonce (code lookup + delete, atomic).
//  2. Looks up the signing device's public key from device_keys.
//  3. Verifies: ed25519.Verify(signingDevicePub, newDevicePub, certificate).
//  4. Inserts the new device key with the certificate.
func (h *deviceHandler) linkVerify(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	var req struct {
		Code            string `json:"code"`
		Certificate     string `json:"certificate"`
		NewDeviceID     string `json:"newDeviceId"`
		SigningDeviceID  string `json:"signingDeviceId"`
		DevicePublicKey string `json:"devicePublicKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	if req.Code == "" || req.NewDeviceID == "" || req.SigningDeviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "code, newDeviceId, and signingDeviceId are required"})
		return
	}

	nonce := linkNoncePrefix + strings.ToUpper(req.Code)
	storedPub, err := h.store.ConsumeAuthNonce(r.Context(), nonce)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "code expired or invalid"})
		return
	}

	// Prefer the stored public key from the nonce; fall back to the request body
	// for the QR flow where the public key arrives in-band.
	newDevicePubBytes := storedPub
	if len(newDevicePubBytes) != ed25519PublicKeyLen && req.DevicePublicKey != "" {
		newDevicePubBytes, err = base64.StdEncoding.DecodeString(req.DevicePublicKey)
		if err != nil || len(newDevicePubBytes) != ed25519PublicKeyLen {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid devicePublicKey"})
			return
		}
	}

	certBytes, err := base64.StdEncoding.DecodeString(req.Certificate)
	if err != nil || len(certBytes) != ed25519CertLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "certificate must be 64 bytes (Ed25519 signature)"})
		return
	}

	signingPub, err := h.findDevicePublicKey(r.Context(), userID, req.SigningDeviceID)
	if err != nil {
		slog.Error("find signing device for link-verify", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not look up signing device"})
		return
	}
	if signingPub == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "signing device not found"})
		return
	}

	if !ed25519.Verify(signingPub, newDevicePubBytes, certBytes) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid certificate"})
		return
	}

	if err := h.store.InsertDeviceKey(r.Context(), userID, req.NewDeviceID, newDevicePubBytes, certBytes); err != nil {
		slog.Error("insert device key via link-verify", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not register device"})
		return
	}

	w.WriteHeader(http.StatusCreated)
}

// ---------- Helpers ----------

// findDevicePublicKey looks up the raw public key for a specific device belonging
// to a user. Returns nil (no error) when the device is not found.
func (h *deviceHandler) findDevicePublicKey(ctx context.Context, userID, deviceID string) ([]byte, error) {
	keys, err := h.store.ListDeviceKeys(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		if k.DeviceID == deviceID {
			return k.DevicePublicKey, nil
		}
	}
	return nil, nil
}

// generateLinkCode returns an 8-character alphanumeric code from [A-Z0-9].
// Uses math/rand which is fine here — the code is short-lived (5 min) and
// the security property is the certificate, not the code confidentiality.
func generateLinkCode() string {
	b := make([]byte, linkCodeLen)
	for i := range b {
		b[i] = linkCodeCharset[rand.Intn(len(linkCodeCharset))]
	}
	return string(b)
}
