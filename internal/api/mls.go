package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"hush.app/server/internal/db"
	"hush.app/server/internal/version"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const maxKeyPackagesPerUpload = 200

// MLSBroadcaster is satisfied by *ws.Hub. Used for dependency injection in tests.
type MLSBroadcaster interface {
	BroadcastToUser(userID string, message []byte)
}

// MLSRoutes returns the router for /api/mls (mount at /api/mls).
// Route order is significant: /key-packages/count must be registered before
// /key-packages/{userId}/{deviceId} to prevent chi matching "count" as a userId.
func MLSRoutes(store db.Store, hub MLSBroadcaster, jwtSecret string) chi.Router {
	r := chi.NewRouter()
	h := &mlsHandler{store: store, hub: hub}
	r.Use(RequireAuth(jwtSecret, store))
	r.Post("/credentials", h.uploadCredential)
	r.Post("/key-packages", h.uploadKeyPackages)
	r.Get("/key-packages/count", h.getKeyPackageCount)
	r.Get("/key-packages/{userId}/{deviceId}", h.consumeKeyPackage)
	return r
}

type mlsHandler struct {
	store db.Store
	hub   MLSBroadcaster
}

// uploadCredentialRequest is the body for POST /api/mls/credentials.
type uploadCredentialRequest struct {
	DeviceID        string `json:"deviceId"`
	CredentialBytes []byte `json:"credentialBytes"`
	SigningPublicKey []byte `json:"signingPublicKey"`
}

// uploadKeyPackagesRequest is the body for POST /api/mls/key-packages.
type uploadKeyPackagesRequest struct {
	DeviceID    string    `json:"deviceId"`
	KeyPackages [][]byte  `json:"keyPackages"`
	ExpiresAt   time.Time `json:"expiresAt"`
	LastResort  bool      `json:"lastResort"`
}

// uploadCredential handles POST /api/mls/credentials.
// Stores the MLS credential and signing public key for the authenticated user's device.
// Returns 204 on success.
func (h *mlsHandler) uploadCredential(w http.ResponseWriter, r *http.Request) {
	var req uploadCredentialRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	if req.DeviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "deviceId is required"})
		return
	}
	if len(req.CredentialBytes) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "credentialBytes is required"})
		return
	}
	if len(req.SigningPublicKey) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "signingPublicKey is required"})
		return
	}

	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	if err := h.store.UpsertMLSCredential(r.Context(), userID, req.DeviceID, req.CredentialBytes, req.SigningPublicKey, 1); err != nil {
		slog.Error("mls: upsert credential", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "upload failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// uploadKeyPackages handles POST /api/mls/key-packages.
// Stores a batch of KeyPackages for the authenticated user's device. When lastResort
// is true and the batch contains exactly one package, it is stored as the last-resort
// KeyPackage (replacing any previous one). Otherwise the batch is inserted as regular
// packages. Returns 204 on success.
func (h *mlsHandler) uploadKeyPackages(w http.ResponseWriter, r *http.Request) {
	var req uploadKeyPackagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	if req.DeviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "deviceId is required"})
		return
	}
	if len(req.KeyPackages) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "keyPackages must not be empty"})
		return
	}

	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	// Last-resort path: single package, caller explicitly flagged it.
	if req.LastResort && len(req.KeyPackages) == 1 {
		if err := h.store.InsertMLSLastResortKeyPackage(r.Context(), userID, req.DeviceID, req.KeyPackages[0]); err != nil {
			slog.Error("mls: insert last-resort key package", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "upload failed"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Regular batch path.
	if len(req.KeyPackages) > maxKeyPackagesPerUpload {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many key packages"})
		return
	}
	if req.ExpiresAt.IsZero() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expiresAt is required"})
		return
	}

	if err := h.store.InsertMLSKeyPackages(r.Context(), userID, req.DeviceID, req.KeyPackages, req.ExpiresAt); err != nil {
		slog.Error("mls: insert key packages", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "upload failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getKeyPackageCount handles GET /api/mls/key-packages/count.
// Returns the number of unused, non-expired, non-last-resort KeyPackages for the
// authenticated user's device. Query parameter: deviceId (required).
func (h *mlsHandler) getKeyPackageCount(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	deviceID := r.URL.Query().Get("deviceId")
	if deviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "deviceId required"})
		return
	}
	count, err := h.store.CountUnusedMLSKeyPackages(r.Context(), userID, deviceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to count key packages"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": count})
}

// consumeKeyPackage handles GET /api/mls/key-packages/{userId}/{deviceId}.
// Atomically consumes one non-expired, non-last-resort KeyPackage for the target
// user+device, falling back to the last-resort package when none remain. Returns 404
// when no package is available. After returning, fires a key_packages.low WS event to
// the target user when their remaining count drops below the threshold.
func (h *mlsHandler) consumeKeyPackage(w http.ResponseWriter, r *http.Request) {
	targetUserID := chi.URLParam(r, "userId")
	deviceID := chi.URLParam(r, "deviceId")
	if targetUserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid userId"})
		return
	}
	if _, err := uuid.Parse(targetUserID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid userId"})
		return
	}
	if deviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "deviceId is required"})
		return
	}

	kpBytes, err := h.store.ConsumeMLSKeyPackage(r.Context(), targetUserID, deviceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to consume key package"})
		return
	}
	if kpBytes == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no key package available"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"keyPackage": base64.StdEncoding.EncodeToString(kpBytes),
	})

	// Fire-and-forget: notify target user when their KeyPackage supply runs low.
	go h.maybeSendKeyPackagesLow(targetUserID, deviceID)
}

// maybeSendKeyPackagesLow broadcasts key_packages.low to the target user when their
// unused KeyPackage count drops below the configured threshold. Fire-and-forget —
// errors and above-threshold counts are silently ignored.
// Called in a goroutine after the response is written, so uses a fresh background context.
func (h *mlsHandler) maybeSendKeyPackagesLow(userID, deviceID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	count, err := h.store.CountUnusedMLSKeyPackages(ctx, userID, deviceID)
	if err != nil || count >= version.KeyPackageLowThreshold {
		return
	}
	msg, _ := json.Marshal(map[string]string{"type": "key_packages.low"})
	h.hub.BroadcastToUser(userID, msg)
}
