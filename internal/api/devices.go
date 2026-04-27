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
	"sync"
	"time"

	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/storage"
	"github.com/hushhq/hush-server/internal/transparency"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const (
	linkCodeLen            = 8
	linkCodeExpiry         = 5 * time.Minute
	linkRequestNoncePrefix = "link:req:"
	linkCodeNoncePrefix    = "link:code:"
	linkClaimNoncePrefix   = "link:claim:"
	linkResultNoncePrefix  = "link:result:"
	ed25519CertLen         = 64

	// approvalRateWindow is the rolling window for soft rate monitoring.
	approvalRateWindow = 10 * time.Minute
	// approvalRateThreshold is the count at which a slog.Warn is emitted.
	// The threshold fires when count >= approvalRateThreshold after an approval.
	approvalRateThreshold = 5

	// errLinkingFailed is the generic message for all cryptographic/key failures
	// in link-verify. A single message prevents information leakage about which
	// check specifically failed (replay, cert mismatch, unknown key).
	errLinkingFailed = "Linking failed. Please try again."
)

var linkCodeCharset = []byte("ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")

type storedLinkRequest struct {
	RequestID        string `json:"requestId"`
	Secret           string `json:"secret"`
	Code             string `json:"code"`
	DeviceID         string `json:"deviceId"`
	DevicePublicKey  string `json:"devicePublicKey"`
	SessionPublicKey string `json:"sessionPublicKey"`
	Label            string `json:"label,omitempty"`
	InstanceURL      string `json:"instanceUrl,omitempty"`
	ExpiresAt        string `json:"expiresAt"`
}

type storedLinkResult struct {
	RelayCiphertext string `json:"relayCiphertext"`
	RelayIV         string `json:"relayIv"`
	RelayPublicKey  string `json:"relayPublicKey"`
	DeviceID        string `json:"deviceId"`
	InstanceURL     string `json:"instanceUrl,omitempty"`
}

// DeviceRoutes returns the chi.Router for device management and device-linking
// endpoints mounted under /api/auth.
//
// Route map:
//
//	Public:
//	  POST /link-request      - new device creates a 5-minute link request
//	  POST /link-result       - new device consumes the encrypted relay payload
//
//	Authenticated:
//	  GET    /devices            - list authenticated user's devices
//	  POST   /devices            - certify a new device directly
//	  DELETE /devices/{deviceId} - revoke a single device
//	  DELETE /devices?all=true   - revoke all devices
//	  POST   /link-resolve       - existing device claims a QR/code request
//	  POST   /link-verify        - existing device certifies + uploads relay payload
//
// transparencySvc may be nil when the transparency log is not configured for
// this instance. Device certify and revoke operations append synchronously
// when the service is non-nil (non-fatal on failure).
//
// hub may be nil; all broadcast calls are nil-checked before use.
func DeviceRoutes(store db.Store, jwtSecret string, transparencySvc *transparency.TransparencyService, hub GlobalBroadcaster) func(r chi.Router) {
	h := newDeviceHandler(store, transparencySvc, hub)
	return h.routes(jwtSecret)
}

// newDeviceHandler builds the deviceHandler without mounting routes; callers
// that need a handle on the handler (for example, to start the link-archive
// purger goroutine) use this directly.
//
// The link-archive plane requires a storage.Backend; the in-process
// helper newDefaultLinkArchiveBackend wires the postgres_bytea backend
// over the same store so the call site below stays simple. Production
// deployments swap this through the env-driven factory at startup.
func newDeviceHandler(store db.Store, transparencySvc *transparency.TransparencyService, hub GlobalBroadcaster) *deviceHandler {
	return &deviceHandler{
		store:           store,
		transparencySvc: transparencySvc,
		hub:             hub,
		backend:         storage.NewPostgresBytea(store),
		approvalTracker: make(map[string][]time.Time),
	}
}

// newDeviceHandlerWithBackend lets the application boot wire a
// non-default backend (e.g. one constructed via storage.LoadConfig +
// storage.NewBackend so STORAGE_BACKEND=s3 takes effect).
func newDeviceHandlerWithBackend(store db.Store, transparencySvc *transparency.TransparencyService, hub GlobalBroadcaster, backend storage.Backend) *deviceHandler {
	h := newDeviceHandler(store, transparencySvc, hub)
	h.backend = backend
	return h
}

// routes returns the chi setup func; lifted out so callers can mount it.
func (h *deviceHandler) routes(jwtSecret string) func(r chi.Router) {
	return func(r chi.Router) {
		// New-device side: no auth session exists yet.
		r.Post("/link-request", h.linkRequest)
		r.Post("/link-result", h.linkResult)

		r.Group(func(r chi.Router) {
			r.Use(RequireAuth(jwtSecret, h.store))

			r.Route("/devices", func(r chi.Router) {
				r.Get("/", h.listDevices)
				r.Post("/", h.certifyDevice)
				r.Delete("/", h.revokeAllDevices)
				r.Delete("/{deviceId}", h.revokeDevice)
			})

			r.Post("/link-resolve", h.linkResolve)
			r.Post("/link-verify", h.linkVerify)
		})

		// Chunked device-link transfer plane. Authenticated upload routes and
		// download-token-gated download routes are mounted side-by-side.
		h.linkArchiveRoutes(r, jwtSecret)
	}
}

type deviceHandler struct {
	store           db.Store
	transparencySvc *transparency.TransparencyService
	// hub is used for downstream WS notifications. May be nil.
	hub GlobalBroadcaster

	// backend stores chunk bytes for the link-archive plane. Always
	// non-nil after newDeviceHandler runs.
	backend storage.Backend

	// approvalTracker tracks per-signing-device approval timestamps for soft
	// rate monitoring. Access is guarded by approvalMu.
	approvalMu      sync.Mutex
	approvalTracker map[string][]time.Time
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
//	  "deviceId":        "<string - caller-assigned device ID>",
//	  "label":           "<string - optional human-readable name>",
//	  "signingDeviceId": "<string - device ID of the authorising device>"
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
		SigningDeviceID string `json:"signingDeviceId"`
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

	req.Label = strings.TrimSpace(req.Label)
	if err := h.store.InsertDeviceKey(r.Context(), userID, req.DeviceID, req.Label, newDevicePubBytes, certBytes); err != nil {
		slog.Error("insert device key", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not register device"})
		return
	}
	if req.Label != "" {
		if err := h.store.UpsertDevice(r.Context(), userID, req.DeviceID, req.Label); err != nil {
			slog.Warn("upsert certified device label", "err", err, "device_id", req.DeviceID)
		}
	}

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

	revokedPub, _ := h.findDevicePublicKey(r.Context(), userID, deviceID)

	if err := h.store.RevokeDeviceKey(r.Context(), userID, deviceID); err != nil {
		slog.Error("revoke device key", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not revoke device"})
		return
	}

	// Kick any in-flight WS connection for the just-revoked device so
	// it cannot keep streaming messages until the next reconnect. The
	// hub-side disconnect is best-effort: subsequent HTTP requests +
	// WS reconnects are already gated by IsDeviceActive in middleware.
	if h.hub != nil {
		h.hub.DisconnectDevice(userID, deviceID)
	}

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

// ---------- Link request (new-device flow) ----------

// linkRequest handles POST /api/auth/link-request.
//
// The new device POSTs:
//   - a fresh Ed25519 device public key (for account association)
//   - a fresh ECDH session public key (for blind-relay encryption)
//   - its stable deviceId
//
// The server creates:
//   - an opaque QR handle (requestId + secret)
//   - an 8-character fallback code for desktop-to-desktop pairing
//
// Both identifiers point to the same stored request and expire after 5 minutes.
func (h *deviceHandler) linkRequest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DevicePublicKey  string `json:"devicePublicKey"`
		SessionPublicKey string `json:"sessionPublicKey"`
		DeviceID         string `json:"deviceId"`
		Label            string `json:"label"`
		InstanceURL      string `json:"instanceUrl"`
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
	sessionPubBytes, err := base64.StdEncoding.DecodeString(req.SessionPublicKey)
	if err != nil || len(sessionPubBytes) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "sessionPublicKey is required"})
		return
	}
	if strings.TrimSpace(req.DeviceID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "deviceId is required"})
		return
	}

	requestID := uuid.NewString()
	secret := uuid.NewString()
	code := generateLinkCode()
	expiresAt := time.Now().Add(linkCodeExpiry).UTC()
	payload := storedLinkRequest{
		RequestID:        requestID,
		Secret:           secret,
		Code:             code,
		DeviceID:         strings.TrimSpace(req.DeviceID),
		DevicePublicKey:  req.DevicePublicKey,
		SessionPublicKey: req.SessionPublicKey,
		Label:            strings.TrimSpace(req.Label),
		InstanceURL:      strings.TrimSpace(req.InstanceURL),
		ExpiresAt:        expiresAt.Format(time.RFC3339),
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create link request"})
		return
	}

	requestNonce := linkRequestNonce(payload.RequestID, payload.Secret)
	codeNonce := linkCodeNonce(payload.Code)
	if err := h.store.InsertAuthNonce(r.Context(), requestNonce, payloadBytes, expiresAt); err != nil {
		slog.Error("insert link request nonce", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create link request"})
		return
	}
	if err := h.store.InsertAuthNonce(r.Context(), codeNonce, payloadBytes, expiresAt); err != nil {
		_ = h.store.DeleteAuthNonce(r.Context(), requestNonce)
		slog.Error("insert link request code nonce", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not create link request"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"requestId": payload.RequestID,
		"secret":    payload.Secret,
		"code":      payload.Code,
		"expiresAt": payload.ExpiresAt,
	})
}

// ---------- Link resolve (existing-device claim step) ----------

// linkResolve handles POST /api/auth/link-resolve.
//
// The existing authenticated device claims a pairing request either by:
//   - requestId + secret (QR route), or
//   - code (desktop fallback)
//
// The request is consumed immediately so the first scan wins. The server then
// creates a short-lived claim token that the same device must present to
// /link-verify after generating the certificate and blind-relay payload.
func (h *deviceHandler) linkResolve(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	var req struct {
		RequestID string `json:"requestId"`
		Secret    string `json:"secret"`
		Code      string `json:"code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	var (
		payloadBytes []byte
		err          error
	)
	usingCode := strings.TrimSpace(req.Code) != ""
	switch {
	case usingCode:
		payloadBytes, err = h.store.ConsumeAuthNonce(r.Context(), linkCodeNonce(req.Code))
	case strings.TrimSpace(req.RequestID) != "" && strings.TrimSpace(req.Secret) != "":
		payloadBytes, err = h.store.ConsumeAuthNonce(r.Context(), linkRequestNonce(req.RequestID, req.Secret))
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "requestId + secret or code is required"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "link request expired or already claimed"})
		return
	}

	payload, err := decodeStoredLinkRequest(payloadBytes)
	if err != nil {
		slog.Error("decode stored link request", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "link request is corrupted"})
		return
	}

	companionNonce := linkRequestNonce(payload.RequestID, payload.Secret)
	if !usingCode {
		companionNonce = linkCodeNonce(payload.Code)
	}
	if err := h.store.DeleteAuthNonce(r.Context(), companionNonce); err != nil {
		slog.Warn("delete companion link nonce", "err", err, "nonce", companionNonce)
	}

	claimToken := uuid.NewString()
	if err := h.store.InsertAuthNonce(
		r.Context(),
		linkClaimNonce(claimToken),
		payloadBytes,
		time.Now().Add(linkCodeExpiry),
	); err != nil {
		slog.Error("insert link claim nonce", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not claim link request"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"claimToken":       claimToken,
		"requestId":        payload.RequestID,
		"deviceId":         payload.DeviceID,
		"devicePublicKey":  payload.DevicePublicKey,
		"sessionPublicKey": payload.SessionPublicKey,
		"label":            payload.Label,
		"instanceUrl":      payload.InstanceURL,
		"expiresAt":        payload.ExpiresAt,
	})
}

// ---------- Link verify (existing-device approval step) ----------

// linkVerify handles POST /api/auth/link-verify.
//
// The existing device POSTs:
//   - claimToken from /link-resolve
//   - certificate = Sign(existingDevicePriv, newDevicePub)
//   - signingDeviceId = authorising device ID
//   - blind-relay envelope (relayPublicKey, relayIv, relayCiphertext)
//
// The server consumes the claim, verifies the certificate, inserts the device
// association, and stores the opaque relay payload for the new device to fetch.
func (h *deviceHandler) linkVerify(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	var req struct {
		ClaimToken      string `json:"claimToken"`
		Certificate     string `json:"certificate"`
		SigningDeviceID string `json:"signingDeviceId"`
		RelayCiphertext string `json:"relayCiphertext"`
		RelayIV         string `json:"relayIv"`
		RelayPublicKey  string `json:"relayPublicKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	if req.ClaimToken == "" || req.Certificate == "" || req.SigningDeviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "claimToken, certificate, and signingDeviceId are required"})
		return
	}
	if req.RelayCiphertext == "" || req.RelayIV == "" || req.RelayPublicKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "relayCiphertext, relayIv, and relayPublicKey are required"})
		return
	}

	requestBytes, err := h.store.ConsumeAuthNonce(r.Context(), linkClaimNonce(req.ClaimToken))
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "link request expired or already used"})
		return
	}

	requestPayload, err := decodeStoredLinkRequest(requestBytes)
	if err != nil {
		slog.Error("decode link verify request payload", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "link request is corrupted"})
		return
	}

	newDevicePubBytes, err := base64.StdEncoding.DecodeString(requestPayload.DevicePublicKey)
	if err != nil || len(newDevicePubBytes) != ed25519PublicKeyLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid devicePublicKey"})
		return
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
	// Both "signing device not found" and "invalid certificate" return the same
	// generic 401 to prevent information leakage about which check failed.
	if signingPub == nil {
		slog.Warn("link-verify rejected: signing device not registered", "user_id", userID, "signing_device_id", req.SigningDeviceID)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": errLinkingFailed})
		return
	}
	if !ed25519.Verify(signingPub, newDevicePubBytes, certBytes) {
		slog.Warn("link-verify rejected: certificate verification failed", "user_id", userID, "signing_device_id", req.SigningDeviceID)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": errLinkingFailed})
		return
	}

	requestPayload.Label = strings.TrimSpace(requestPayload.Label)
	if err := h.store.InsertDeviceKey(r.Context(), userID, requestPayload.DeviceID, requestPayload.Label, newDevicePubBytes, certBytes); err != nil {
		slog.Error("insert device key via link-verify", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not register device"})
		return
	}

	// Soft approval rate monitoring: log a warning when a single device approves
	// an unusual number of link requests within a short window. No hard block.
	h.recordApproval(req.SigningDeviceID)
	if requestPayload.Label != "" {
		if err := h.store.UpsertDevice(r.Context(), userID, requestPayload.DeviceID, requestPayload.Label); err != nil {
			slog.Warn("upsert linked device label", "err", err, "device_id", requestPayload.DeviceID)
		}
	}

	resultPayload := storedLinkResult{
		RelayCiphertext: req.RelayCiphertext,
		RelayIV:         req.RelayIV,
		RelayPublicKey:  req.RelayPublicKey,
		DeviceID:        requestPayload.DeviceID,
		InstanceURL:     requestPayload.InstanceURL,
	}
	resultBytes, err := json.Marshal(resultPayload)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not finalize link"})
		return
	}
	if err := h.store.InsertAuthNonce(
		r.Context(),
		linkResultNonce(requestPayload.RequestID, requestPayload.Secret),
		resultBytes,
		time.Now().Add(linkCodeExpiry),
	); err != nil {
		slog.Error("insert link result nonce", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not finalize link"})
		return
	}

	if h.transparencySvc != nil {
		entry := &transparency.LogEntry{
			OperationType: transparency.OpDeviceAdd,
			UserPublicKey: signingPub,
			SubjectKey:    newDevicePubBytes,
			Timestamp:     time.Now().Unix(),
		}
		if logErr := h.transparencySvc.AppendAndNotify(r.Context(), entry, userID); logErr != nil {
			slog.Error("transparency: append device_add entry via link-verify", "err", logErr, "user_id", userID)
		}
	}

	w.WriteHeader(http.StatusCreated)
}

// ---------- Link result (new-device fetch step) ----------

// linkResult handles POST /api/auth/link-result.
//
// The new device polls with { requestId, secret } until the existing device
// approves the pairing. Once available, the encrypted relay payload is consumed
// and returned. Missing results are reported as "pending"; the new device uses
// its own local 5-minute timer to decide when the request has expired.
func (h *deviceHandler) linkResult(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RequestID string `json:"requestId"`
		Secret    string `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if strings.TrimSpace(req.RequestID) == "" || strings.TrimSpace(req.Secret) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "requestId and secret are required"})
		return
	}

	resultBytes, err := h.store.ConsumeAuthNonce(r.Context(), linkResultNonce(req.RequestID, req.Secret))
	if err != nil {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
		return
	}

	var result storedLinkResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		slog.Error("decode link result payload", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "link result is corrupted"})
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// ---------- Helpers ----------

// recordApproval tracks a successful link-verify approval from signingDeviceID
// and emits a slog.Warn when the count within the rolling window reaches
// approvalRateThreshold. This is a soft signal only — the request is never blocked.
func (h *deviceHandler) recordApproval(signingDeviceID string) {
	now := time.Now()
	cutoff := now.Add(-approvalRateWindow)

	h.approvalMu.Lock()
	defer h.approvalMu.Unlock()

	// Prune timestamps older than the rolling window.
	existing := h.approvalTracker[signingDeviceID]
	fresh := existing[:0]
	for _, ts := range existing {
		if ts.After(cutoff) {
			fresh = append(fresh, ts)
		}
	}
	fresh = append(fresh, now)
	h.approvalTracker[signingDeviceID] = fresh

	if len(fresh) >= approvalRateThreshold {
		slog.Warn("unusual device approval rate",
			"signing_device_id", signingDeviceID,
			"count", len(fresh),
		)
	}
}

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

func decodeStoredLinkRequest(payload []byte) (*storedLinkRequest, error) {
	var request storedLinkRequest
	if err := json.Unmarshal(payload, &request); err != nil {
		return nil, err
	}
	return &request, nil
}

func linkRequestNonce(requestID, secret string) string {
	return linkRequestNoncePrefix + requestID + ":" + secret
}

func linkCodeNonce(code string) string {
	return linkCodeNoncePrefix + strings.ToUpper(strings.TrimSpace(code))
}

func linkClaimNonce(claimToken string) string {
	return linkClaimNoncePrefix + claimToken
}

func linkResultNonce(requestID, secret string) string {
	return linkResultNoncePrefix + requestID + ":" + secret
}

// generateLinkCode returns an 8-character alphanumeric code from [A-Z0-9].
// Uses math/rand which is fine here - the code is short-lived (5 min) and
// the security property is the certificate, not the code confidentiality.
func generateLinkCode() string {
	b := make([]byte, linkCodeLen)
	for i := range b {
		b[i] = linkCodeCharset[rand.Intn(len(linkCodeCharset))]
	}
	return string(b)
}
