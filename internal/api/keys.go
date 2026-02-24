package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"hush.app/server/internal/db"
	"hush.app/server/internal/models"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

const lowPreKeyThreshold = 10
const maxOneTimePreKeysPerUpload = 200

// KeysBroadcaster is satisfied by *ws.Hub. Used for dependency injection in tests.
type KeysBroadcaster interface {
	BroadcastToUser(userID string, message []byte)
}

// KeysRoutes returns the router for /api/keys (mount at /api/keys).
func KeysRoutes(store db.Store, hub KeysBroadcaster, jwtSecret string) chi.Router {
	r := chi.NewRouter()
	h := &keysHandler{store: store, hub: hub}
	r.Use(RequireAuth(jwtSecret, store))
	r.Post("/upload", h.upload)
	r.Get("/{userId}/{deviceId}", h.getByUserDevice)
	r.Get("/{userId}", h.getByUser)
	return r
}

type keysHandler struct {
	store db.Store
	hub   KeysBroadcaster
}

func (h *keysHandler) upload(w http.ResponseWriter, r *http.Request) {
	var req models.PreKeyUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	if req.DeviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "deviceId is required"})
		return
	}
	if len(req.IdentityKey) == 0 || len(req.SignedPreKey) == 0 || len(req.SignedPreKeySignature) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "identityKey, signedPreKey, and signedPreKeySignature are required"})
		return
	}
	if len(req.OneTimePreKeys) > maxOneTimePreKeysPerUpload {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many one-time pre-keys"})
		return
	}

	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	if err := h.store.UpsertIdentityKeys(r.Context(), userID, req.DeviceID, req.IdentityKey, req.SignedPreKey, req.SignedPreKeySignature, req.RegistrationID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "upload failed"})
		return
	}
	if len(req.OneTimePreKeys) > 0 {
		if err := h.store.InsertOneTimePreKeys(r.Context(), userID, req.DeviceID, req.OneTimePreKeys); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "upload failed"})
			return
		}
	}
	if err := h.store.UpsertDevice(r.Context(), userID, req.DeviceID, ""); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "upload failed"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *keysHandler) getByUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid userId"})
		return
	}
	if _, err := uuid.Parse(userID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid userId"})
		return
	}
	deviceIDs, err := h.store.ListDeviceIDsForUser(r.Context(), userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list devices"})
		return
	}
	if len(deviceIDs) == 0 {
		writeJSON(w, http.StatusOK, []models.PreKeyBundle{})
		return
	}
	var bundles []models.PreKeyBundle
	var dbErrors int
	for _, deviceID := range deviceIDs {
		bundle, err := h.buildBundle(r.Context(), userID, deviceID)
		if err != nil {
			slog.Error("buildBundle failed", "userID", userID, "deviceID", deviceID, "error", err)
			dbErrors++
			continue
		}
		if bundle != nil {
			bundles = append(bundles, *bundle)
			h.maybeSendKeysLow(r.Context(), userID, deviceID)
		}
	}
	if dbErrors > 0 && len(bundles) == 0 {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to retrieve bundles"})
		return
	}
	writeJSON(w, http.StatusOK, bundles)
}

func (h *keysHandler) getByUserDevice(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userId")
	deviceID := chi.URLParam(r, "deviceId")
	if userID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid userId"})
		return
	}
	if _, err := uuid.Parse(userID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid userId"})
		return
	}
	if deviceID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "deviceId is required"})
		return
	}
	bundle, err := h.buildBundle(r.Context(), userID, deviceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get bundle"})
		return
	}
	if bundle == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no bundle for user and device"})
		return
	}
	h.maybeSendKeysLow(r.Context(), userID, deviceID)
	writeJSON(w, http.StatusOK, bundle)
}

func (h *keysHandler) buildBundle(ctx context.Context, userID, deviceID string) (*models.PreKeyBundle, error) {
	identityKey, signedPreKey, sig, regID, err := h.store.GetIdentityAndSignedPreKey(ctx, userID, deviceID)
	if err != nil {
		return nil, err
	}
	if identityKey == nil {
		return nil, nil
	}
	bundle := &models.PreKeyBundle{
		IdentityKey:           identityKey,
		SignedPreKey:          signedPreKey,
		SignedPreKeySignature: sig,
		RegistrationID:        regID,
	}
	keyID, pubKey, err := h.store.ConsumeOneTimePreKey(ctx, userID, deviceID)
	if err == nil {
		bundle.OneTimePreKeyID = &keyID
		bundle.OneTimePreKey = pubKey
	}
	return bundle, nil
}

func (h *keysHandler) maybeSendKeysLow(ctx context.Context, userID, deviceID string) {
	count, err := h.store.CountUnusedOneTimePreKeys(ctx, userID, deviceID)
	if err != nil || count >= lowPreKeyThreshold {
		return
	}
	msg, _ := json.Marshal(map[string]string{"type": "keys.low"})
	h.hub.BroadcastToUser(userID, msg)
}

