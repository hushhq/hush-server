package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	Broadcast(channelID string, message []byte, excludeClientID string)
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
	r.Get("/groups/{channelId}/info", h.getGroupInfo)
	r.Put("/groups/{channelId}/info", h.putGroupInfo)
	r.Post("/groups/{channelId}/commit", h.postCommit)
	r.Get("/groups/{channelId}/commits", h.getCommitsSinceEpoch)
	r.Get("/pending-welcomes", h.getPendingWelcomes)
	r.Delete("/pending-welcomes/{welcomeId}", h.deletePendingWelcome)
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

// getGroupInfoRequest is the response body for GET /api/mls/groups/:channelId/info.
type getGroupInfoResponse struct {
	GroupInfo string `json:"groupInfo"`
	Epoch     int64  `json:"epoch"`
}

// putGroupInfoRequest is the request body for PUT /api/mls/groups/:channelId/info.
type putGroupInfoRequest struct {
	GroupInfo string `json:"groupInfo"`
	Epoch     int64  `json:"epoch"`
}

// postCommitRequest is the request body for POST /api/mls/groups/:channelId/commit.
type postCommitRequest struct {
	CommitBytes string `json:"commitBytes"`
	GroupInfo   string `json:"groupInfo"`
	Epoch       int64  `json:"epoch"`
}

// getGroupInfo handles GET /api/mls/groups/:channelId/info.
// Returns the current MLS GroupInfo bytes (base64) and epoch for a channel.
// Returns 404 when the channel has no group yet.
func (h *mlsHandler) getGroupInfo(w http.ResponseWriter, r *http.Request) {
	channelID := chi.URLParam(r, "channelId")
	if _, err := uuid.Parse(channelID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid channelId"})
		return
	}

	groupInfoBytes, epoch, err := h.store.GetMLSGroupInfo(r.Context(), channelID)
	if err != nil {
		slog.Error("mls: get group info", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get group info"})
		return
	}
	if groupInfoBytes == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "group not found"})
		return
	}

	writeJSON(w, http.StatusOK, getGroupInfoResponse{
		GroupInfo: base64.StdEncoding.EncodeToString(groupInfoBytes),
		Epoch:     epoch,
	})
}

// putGroupInfo handles PUT /api/mls/groups/:channelId/info.
// Upserts the GroupInfo bytes and epoch for a channel. Returns 204 on success.
func (h *mlsHandler) putGroupInfo(w http.ResponseWriter, r *http.Request) {
	channelID := chi.URLParam(r, "channelId")
	if _, err := uuid.Parse(channelID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid channelId"})
		return
	}

	var req putGroupInfoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.GroupInfo == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "groupInfo is required"})
		return
	}

	groupInfoBytes, err := base64.StdEncoding.DecodeString(req.GroupInfo)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid groupInfo base64"})
		return
	}
	if len(groupInfoBytes) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "groupInfo must not be empty"})
		return
	}

	if err := h.store.UpsertMLSGroupInfo(r.Context(), channelID, groupInfoBytes, req.Epoch); err != nil {
		slog.Error("mls: upsert group info", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to store group info"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// postCommit handles POST /api/mls/groups/:channelId/commit.
// Stores the Commit, updates GroupInfo, and broadcasts mls.commit to channel subscribers.
// Returns 204 on success.
func (h *mlsHandler) postCommit(w http.ResponseWriter, r *http.Request) {
	channelID := chi.URLParam(r, "channelId")
	if _, err := uuid.Parse(channelID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid channelId"})
		return
	}

	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	var req postCommitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.CommitBytes == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "commitBytes is required"})
		return
	}
	if req.GroupInfo == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "groupInfo is required"})
		return
	}

	commitBytes, err := base64.StdEncoding.DecodeString(req.CommitBytes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid commitBytes base64"})
		return
	}
	groupInfoBytes, err := base64.StdEncoding.DecodeString(req.GroupInfo)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid groupInfo base64"})
		return
	}

	ctx := r.Context()
	if err := h.store.UpsertMLSGroupInfo(ctx, channelID, groupInfoBytes, req.Epoch); err != nil {
		slog.Error("mls: upsert group info on commit", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to store group info"})
		return
	}
	if err := h.store.AppendMLSCommit(ctx, channelID, req.Epoch, commitBytes, userID); err != nil {
		slog.Error("mls: append commit", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to store commit"})
		return
	}

	msg, _ := json.Marshal(map[string]interface{}{
		"type":         "mls.commit",
		"channel_id":   channelID,
		"epoch":        req.Epoch,
		"commit_bytes": req.CommitBytes,
		"sender_id":    userID,
	})
	h.hub.Broadcast(channelID, msg, "")
	w.WriteHeader(http.StatusNoContent)
}

// commitResponseItem represents one commit in the GET commits response.
type commitResponseItem struct {
	Epoch       int64  `json:"epoch"`
	CommitBytes string `json:"commitBytes"`
	SenderID    string `json:"senderId"`
	CreatedAt   string `json:"createdAt"`
}

const (
	commitsDefaultLimit = 100
	commitsMaxLimit     = 1000
)

// getCommitsSinceEpoch handles GET /api/mls/groups/:channelId/commits.
// Returns a list of Commits with epoch > since_epoch, ordered ascending.
// Query params: since_epoch (int64, default 0), limit (int, default 100, max 1000).
func (h *mlsHandler) getCommitsSinceEpoch(w http.ResponseWriter, r *http.Request) {
	channelID := chi.URLParam(r, "channelId")
	if _, err := uuid.Parse(channelID); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid channelId"})
		return
	}

	var sinceEpoch int64
	if s := r.URL.Query().Get("since_epoch"); s != "" {
		if _, err := fmt.Sscanf(s, "%d", &sinceEpoch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid since_epoch"})
			return
		}
	}

	limit := commitsDefaultLimit
	if l := r.URL.Query().Get("limit"); l != "" {
		if _, err := fmt.Sscanf(l, "%d", &limit); err != nil || limit <= 0 {
			limit = commitsDefaultLimit
		}
		if limit > commitsMaxLimit {
			limit = commitsMaxLimit
		}
	}

	commits, err := h.store.GetMLSCommitsSinceEpoch(r.Context(), channelID, sinceEpoch, limit)
	if err != nil {
		slog.Error("mls: get commits since epoch", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get commits"})
		return
	}

	items := make([]commitResponseItem, 0, len(commits))
	for _, c := range commits {
		items = append(items, commitResponseItem{
			Epoch:       c.Epoch,
			CommitBytes: base64.StdEncoding.EncodeToString(c.CommitBytes),
			SenderID:    c.SenderID,
			CreatedAt:   c.CreatedAt.Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"commits": items})
}

// pendingWelcomeResponseItem represents one pending Welcome in the GET response.
type pendingWelcomeResponseItem struct {
	ID           string `json:"id"`
	ChannelID    string `json:"channelId"`
	WelcomeBytes string `json:"welcomeBytes"`
	SenderID     string `json:"senderId"`
	Epoch        int64  `json:"epoch"`
	CreatedAt    string `json:"createdAt"`
}

// getPendingWelcomes handles GET /api/mls/pending-welcomes.
// Returns all pending Welcome messages for the authenticated user.
func (h *mlsHandler) getPendingWelcomes(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	welcomes, err := h.store.GetPendingWelcomes(r.Context(), userID)
	if err != nil {
		slog.Error("mls: get pending welcomes", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to get pending welcomes"})
		return
	}

	items := make([]pendingWelcomeResponseItem, 0, len(welcomes))
	for _, w := range welcomes {
		items = append(items, pendingWelcomeResponseItem{
			ID:           w.ID,
			ChannelID:    w.ChannelID,
			WelcomeBytes: base64.StdEncoding.EncodeToString(w.WelcomeBytes),
			SenderID:     w.SenderID,
			Epoch:        w.Epoch,
			CreatedAt:    w.CreatedAt.Format(time.RFC3339Nano),
		})
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"welcomes": items})
}

// deletePendingWelcome handles DELETE /api/mls/pending-welcomes/:welcomeId.
// Removes a specific pending Welcome after the client ACKs it. Returns 204 on success.
func (h *mlsHandler) deletePendingWelcome(w http.ResponseWriter, r *http.Request) {
	welcomeID := chi.URLParam(r, "welcomeId")
	if welcomeID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "welcomeId is required"})
		return
	}

	if err := h.store.DeletePendingWelcome(r.Context(), welcomeID); err != nil {
		slog.Error("mls: delete pending welcome", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete pending welcome"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
