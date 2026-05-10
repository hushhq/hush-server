package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/storage"
)

// MaxAttachmentBytes caps the ciphertext byte size of a single
// attachment. The blob is already AES-GCM-encrypted on the client, so
// the server cannot inspect the plaintext — this ceiling exists for
// quota/abuse reasons only. Mirrors the client-side cap in
// `src/lib/attachmentLimits.ts`.
const MaxAttachmentBytes = 25 * 1024 * 1024

// presignTTL is the validity window of upload + download URLs handed
// back to the client. Keep short so a leaked URL stops working before
// it can be widely distributed; the client renews on-demand.
const presignTTL = 5 * time.Minute

// allowedAttachmentContentTypes is matched as a prefix-or-exact set.
// `image/`, `audio/`, `video/`, `text/` accept any subtype; the others
// are exact matches. This mirrors the client allowlist; both sides
// must agree or sends silently fail at presign time.
var allowedAttachmentContentTypes = []string{
	"image/",
	"audio/",
	"video/mp4",
	"video/webm",
	"text/",
	"application/pdf",
}

func contentTypeAllowed(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct == "" {
		return false
	}
	for _, prefix := range allowedAttachmentContentTypes {
		if strings.HasSuffix(prefix, "/") {
			if strings.HasPrefix(ct, prefix) {
				return true
			}
			continue
		}
		if ct == prefix {
			return true
		}
	}
	return false
}

// AttachmentBackendFactory constructs the storage Backend used for
// attachment presign URLs. Injected so tests can hand a fake; in
// production main.go wires the same singleton it uses for link-archive
// chunks. A nil factory disables the routes (503).
type AttachmentBackendFactory func() (storage.Backend, error)

// ChannelAttachmentRoutes mounts the per-channel presign endpoint:
//
//	POST /presign  -> presign upload, insert row, return { id, uploadUrl, ... }
//
// Mounted by ChannelRoutes under /api/servers/{serverId}/channels/{id}/attachments.
// Auth + RequireGuildMember are inherited from the parent server router.
func ChannelAttachmentRoutes(store db.Store, backendFactory AttachmentBackendFactory) chi.Router {
	r := chi.NewRouter()
	h := &attachmentHandler{store: store, backendFactory: backendFactory}
	r.Post("/presign", h.presign)
	return r
}

// AttachmentRoutes mounts the global download/delete endpoints:
//
//	GET    /{id}/download
//	DELETE /{id}
//
// Mounted at /api/attachments. Auth applied here; channel-membership
// for download is checked per-row inside the handler.
func AttachmentRoutes(store db.Store, backendFactory AttachmentBackendFactory, jwtSecret string) chi.Router {
	r := chi.NewRouter()
	r.Use(RequireAuth(jwtSecret, store))
	h := &attachmentHandler{store: store, backendFactory: backendFactory}
	r.Get("/{id}/download", h.download)
	r.Delete("/{id}", h.delete)
	return r
}

type attachmentHandler struct {
	store          db.Store
	backendFactory AttachmentBackendFactory
}

type presignRequest struct {
	Size        int64  `json:"size"`
	ContentType string `json:"contentType"`
}

type presignResponse struct {
	ID        string            `json:"id"`
	UploadURL string            `json:"uploadUrl"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt time.Time         `json:"expiresAt"`
}

type downloadResponse struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

func (h *attachmentHandler) presign(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	channelID := chi.URLParam(r, "channelId")
	if channelID == "" {
		// Routes are mounted under {id} for channels; some chi configs
		// expose the param under "id" instead of "channelId". Check both.
		channelID = chi.URLParam(r, "id")
	}
	if channelID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing channel id"})
		return
	}

	var req presignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Size <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "size must be positive"})
		return
	}
	cfg, err := h.store.GetInstanceConfig(r.Context())
	if err != nil {
		slog.Error("attachment presign: GetInstanceConfig", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config lookup failed"})
		return
	}
	maxBytes := cfg.MaxAttachmentBytes
	if maxBytes <= 0 {
		maxBytes = MaxAttachmentBytes
	}
	if req.Size > maxBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"error": fmt.Sprintf("attachment exceeds %d bytes", maxBytes),
		})
		return
	}
	if cfg.MaxGuildAttachmentStorageBytes != nil && req.Size > *cfg.MaxGuildAttachmentStorageBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"error": fmt.Sprintf("attachment exceeds guild storage quota of %d bytes", *cfg.MaxGuildAttachmentStorageBytes),
		})
		return
	}
	if !contentTypeAllowed(req.ContentType) {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{
			"error": "content type not allowed",
		})
		return
	}

	backend, err := h.resolveBackend()
	if err != nil {
		slog.Error("attachment presign: backend unavailable", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "attachment storage is not configured on this instance",
		})
		return
	}

	// Verify channel membership defensively even though the parent
	// router applies RequireGuildMember — the channel might belong to
	// a different guild than the server in the URL, in which case the
	// guild-level check passes but the user is not actually in the
	// channel's owning guild.
	ok, err := h.store.IsChannelMember(r.Context(), channelID, userID)
	if err != nil {
		slog.Error("attachment presign: IsChannelMember", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "membership check failed"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a channel member"})
		return
	}

	storageKey := buildAttachmentKey(channelID)
	row, err := h.store.InsertAttachment(r.Context(), channelID, userID, storageKey, req.ContentType, req.Size)
	if err != nil {
		slog.Error("attachment presign: InsertAttachment", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to record attachment"})
		return
	}
	if cfg.MaxGuildAttachmentStorageBytes != nil {
		if err := h.enforceGuildAttachmentQuota(r.Context(), backend, channelID, *cfg.MaxGuildAttachmentStorageBytes); err != nil {
			if _, sdErr := h.store.SoftDeleteAttachment(r.Context(), row.ID, userID); sdErr != nil && !errors.Is(sdErr, pgx.ErrNoRows) {
				slog.Warn("attachment presign: soft-delete after quota failure", "id", row.ID, "err", sdErr)
			}
			slog.Error("attachment presign: enforce guild attachment quota", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to enforce attachment quota"})
			return
		}
	}
	// Re-key the storage path under the freshly-minted row id so that
	// every blob is namespaced by both channel and attachment id; this
	// avoids a subtle replay where two presigns landing on the same
	// random suffix would race.
	finalKey := fmt.Sprintf("attachments/%s/%s", channelID, row.ID)
	if finalKey != storageKey {
		// Update the row to reflect the canonical key. Skipped here:
		// we built `storageKey` already from a UUID prefix, and the row
		// id is a different UUID, so we keep the inserted key as-is and
		// always use row.StorageKey downstream.
		_ = finalKey
	}

	url, err := backend.PresignPut(r.Context(), row.StorageKey, presignTTL)
	if err != nil {
		// Best-effort cleanup: tombstone the row so the orphan is not
		// visible to readers. Errors here are logged but not fatal —
		// the row stays orphaned and the supervised purger can clean
		// it up after the TTL.
		if _, sdErr := h.store.SoftDeleteAttachment(r.Context(), row.ID, userID); sdErr != nil && !errors.Is(sdErr, pgx.ErrNoRows) {
			slog.Warn("attachment presign: soft-delete after PresignPut failure", "id", row.ID, "err", sdErr)
		}
		slog.Error("attachment presign: backend.PresignPut", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "presign failed"})
		return
	}

	writeJSON(w, http.StatusOK, presignResponse{
		ID:        row.ID,
		UploadURL: url.URL,
		Method:    methodOrDefault(url.Method, http.MethodPut),
		Headers:   url.Headers,
		ExpiresAt: url.ExpiresAt,
	})
}

func (h *attachmentHandler) download(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing attachment id"})
		return
	}
	row, err := h.store.GetAttachmentByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "attachment not found"})
			return
		}
		slog.Error("attachment download: GetAttachmentByID", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
		return
	}
	if row.DeletedAt != nil {
		writeJSON(w, http.StatusGone, map[string]string{"error": "attachment no longer available"})
		return
	}
	ok, err := h.store.IsChannelMember(r.Context(), row.ChannelID, userID)
	if err != nil {
		slog.Error("attachment download: IsChannelMember", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "membership check failed"})
		return
	}
	if !ok {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a channel member"})
		return
	}

	backend, err := h.resolveBackend()
	if err != nil {
		slog.Error("attachment download: backend unavailable", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "attachment storage is not configured on this instance",
		})
		return
	}

	url, err := backend.PresignGet(r.Context(), row.StorageKey, presignTTL)
	if err != nil {
		slog.Error("attachment download: backend.PresignGet", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "presign failed"})
		return
	}
	writeJSON(w, http.StatusOK, downloadResponse{
		URL:       url.URL,
		ExpiresAt: url.ExpiresAt,
	})
}

func (h *attachmentHandler) delete(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing attachment id"})
		return
	}
	row, err := h.store.SoftDeleteAttachment(r.Context(), id, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "attachment not found"})
			return
		}
		slog.Error("attachment delete: SoftDeleteAttachment", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		return
	}

	// Best-effort backend delete. Failure is logged but does not fail
	// the request — the soft-delete is what the user observed and the
	// supervised purger retries the blob removal on a schedule.
	backend, bErr := h.resolveBackend()
	if bErr == nil {
		if delErr := backend.Delete(context.Background(), row.StorageKey); delErr != nil {
			slog.Warn("attachment delete: backend.Delete", "id", row.ID, "err", delErr)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *attachmentHandler) resolveBackend() (storage.Backend, error) {
	if h.backendFactory == nil {
		return nil, errors.New("no attachment backend factory configured")
	}
	return h.backendFactory()
}

func (h *attachmentHandler) enforceGuildAttachmentQuota(ctx context.Context, backend storage.Backend, channelID string, quotaBytes int64) error {
	if quotaBytes <= 0 {
		return nil
	}
	_, active, err := h.store.ListAttachmentsForGuildQuota(ctx, channelID)
	if err != nil {
		return err
	}
	var total int64
	for _, attachment := range active {
		total += attachment.Size
	}
	if total <= quotaBytes {
		return nil
	}
	var (
		toDeleteIDs []string
		excess      = total - quotaBytes
	)
	for _, attachment := range active {
		if excess <= 0 {
			break
		}
		toDeleteIDs = append(toDeleteIDs, attachment.ID)
		excess -= attachment.Size
	}
	deleted, err := h.store.SoftDeleteAttachmentsByID(ctx, toDeleteIDs)
	if err != nil {
		return err
	}
	for _, attachment := range deleted {
		if err := backend.Delete(context.Background(), attachment.StorageKey); err != nil {
			slog.Warn("attachment quota: backend.Delete", "id", attachment.ID, "err", err)
		}
	}
	return nil
}

func methodOrDefault(m, def string) string {
	if m == "" {
		return def
	}
	return m
}

// buildAttachmentKey returns a fresh storage key for an attachment.
// The layout is `attachments/{channelId}/{randomUUID}` so blobs are
// namespaced per-channel for easy bulk-delete on channel removal and
// each upload gets a unique key without colliding across retries.
func buildAttachmentKey(channelID string) string {
	return fmt.Sprintf("attachments/%s/%s", channelID, uuid.NewString())
}
