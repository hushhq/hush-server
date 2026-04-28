package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hushhq/hush-server/internal/db"
	"github.com/hushhq/hush-server/internal/storage"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

// Chunked device-link transfer constants.
//
// chunkSize is the per-chunk plaintext bound; ciphertext adds the 16-byte
// AES-GCM tag. The body limit on the in-API upload fallback path adds a
// small slack for the tag and any client-side framing.
//
// There is no fixed cap on totalChunks or totalBytes — operational
// containment is enforced by per-user quota and per-instance staging
// cap, both configured from instance config (see archiveQuotaForUser
// and instanceStagingBytesCap).
const (
	linkArchiveChunkSize          = 4 * 1024 * 1024 // 4 MiB
	linkArchiveChunkBodyMax       = linkArchiveChunkSize + 256
	linkArchiveTokenBytes         = 32
	linkArchiveDefaultExpiry      = 60 * time.Minute
	linkArchiveMaxLifetime        = 7 * 24 * time.Hour
	linkArchivePurgeInterval      = 5 * time.Minute
	linkArchivePurgeContextSecs   = 30
	linkArchivePurgeBatchLimit    = 64
	linkArchiveUploadTokenHeader  = "X-Upload-Token"
	linkArchiveDownloadHeader     = "X-Download-Token"
	linkArchiveChunkHashHeader    = "X-Chunk-Sha256"
	linkArchiveDefaultUserQuota   = 1
	linkArchiveDefaultStagingCap  = int64(8 * 1024 * 1024 * 1024) // 8 GiB
	// Presigned URL window: how many chunks at a time get URLs minted.
	linkArchiveWindowSize         = 8
	// Presigned URL TTL: long enough for a slow connection to finish a
	// chunk PUT/GET; short enough that a stolen URL has minimal value.
	linkArchivePresignTTL         = 15 * time.Minute
	// Auto-supersede grace window: at /link-archive-init, prior archives
	// owned by the same user that have not seen a sliding-expiry refresh
	// touch within this window are treated as abandoned and torn down
	// before the per-user concurrent-archive quota is checked. Sized to
	// outlast the slowest realistic single-chunk download (4 MiB at
	// ~32 kbps ≈ 17 min) plus margin, so a genuinely-active session is
	// never preempted. Operator override:
	// LINK_ARCHIVE_SUPERSEDE_GRACE_SECONDS env var.
	linkArchiveDefaultSupersedeGrace = 30 * time.Minute
)

// archiveQuotaForUser returns the maximum number of concurrent active
// archives a single user may hold. Operator override:
// LINK_ARCHIVE_USER_QUOTA env var.
func archiveQuotaForUser() int {
	if v := os.Getenv("LINK_ARCHIVE_USER_QUOTA"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return linkArchiveDefaultUserQuota
}

// archiveSupersedeGrace returns the abandonment grace window after which
// a prior active archive owned by the same user is auto-aborted at the
// start of /link-archive-init. Operator override:
// LINK_ARCHIVE_SUPERSEDE_GRACE_SECONDS env var.
func archiveSupersedeGrace() time.Duration {
	if v := os.Getenv("LINK_ARCHIVE_SUPERSEDE_GRACE_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return linkArchiveDefaultSupersedeGrace
}

// instanceStagingBytesCap returns the maximum total_bytes summed across
// all non-terminal archives on the instance. Operator override:
// LINK_ARCHIVE_STAGING_BYTES_CAP env var (raw decimal).
func instanceStagingBytesCap() int64 {
	if v := os.Getenv("LINK_ARCHIVE_STAGING_BYTES_CAP"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return linkArchiveDefaultStagingCap
}

// linkArchiveRoutes mounts the chunked-transfer endpoints inside DeviceRoutes.
func (h *deviceHandler) linkArchiveRoutes(r chi.Router, jwtSecret string) {
	// Authenticated upload plane.
	r.Group(func(r chi.Router) {
		r.Use(RequireAuth(jwtSecret, h.store))
		r.Post("/link-archive-init", h.linkArchiveInit)
		r.Post("/link-archive-upload-window/{archiveId}", h.linkArchiveUploadWindow)
		r.Post("/link-archive-confirm-chunk/{archiveId}/{idx}", h.linkArchiveConfirmChunk)
		r.Put("/link-archive-chunk/{archiveId}/{idx}", h.linkArchiveUploadChunk)
		r.Post("/link-archive-finalize/{archiveId}", h.linkArchiveFinalize)
	})

	// Unauthenticated download plane (download-token-gated).
	r.Get("/link-archive-manifest/{archiveId}", h.linkArchiveManifest)
	r.Post("/link-archive-download-window/{archiveId}", h.linkArchiveDownloadWindow)
	r.Get("/link-archive-chunk/{archiveId}/{idx}", h.linkArchiveDownloadChunk)
	r.Post("/link-archive-ack/{archiveId}", h.linkArchiveAck)
	r.Delete("/link-archive/{archiveId}", h.linkArchiveDelete)
}

// ---------- Init ----------

// linkArchiveInit handles POST /api/auth/link-archive-init.
//
// Operational containment runs before any allocation: per-user quota
// then per-instance staging-bytes ceiling. Either rejects with 4xx
// before a row is created.
func (h *deviceHandler) linkArchiveInit(w http.ResponseWriter, r *http.Request) {
	userID := userIDFromContext(r.Context())
	if userID == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}

	var req struct {
		TotalChunks   int    `json:"totalChunks"`
		TotalBytes    int64  `json:"totalBytes"`
		ChunkSize     int    `json:"chunkSize"`
		ManifestHash  string `json:"manifestHash"`
		ArchiveSHA256 string `json:"archiveSha256"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

	if req.ChunkSize != linkArchiveChunkSize {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chunkSize must equal server-fixed value"})
		return
	}
	if req.TotalChunks <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "totalChunks must be positive"})
		return
	}
	if req.TotalBytes <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "totalBytes must be positive"})
		return
	}
	// `chunkSize` is the PLAINTEXT slice size. Each chunk's encrypted upload
	// body is bounded by `linkArchiveChunkBodyMax = chunkSize + 256` (the
	// per-chunk PUT limit enforced separately when the client uploads
	// each chunk), which already accounts for gzip framing + AES-GCM
	// tag overhead. The aggregate constraint here must use that same
	// per-chunk ciphertext ceiling, otherwise an honest client whose
	// every chunk is full plaintext (incompressible payloads such as
	// already-encrypted transcript blobs) would be incorrectly rejected
	// here while still being well within the per-chunk upload limit.
	if req.TotalBytes > int64(req.TotalChunks)*int64(linkArchiveChunkBodyMax) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "totalBytes inconsistent with totalChunks * chunkBodyMax"})
		return
	}

	manifestHash, err := decodeHashB64(req.ManifestHash)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "manifestHash must be base64 sha256"})
		return
	}
	archiveSHA256, err := decodeHashB64(req.ArchiveSHA256)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "archiveSha256 must be base64 sha256"})
		return
	}

	// Auto-supersede prior abandoned archives (server-side safety net for
	// the case where the NEW-device tab dies before the client-side
	// import-failure cleanup DELETE has a chance to run). Only archives
	// whose last sliding-expiry refresh predates the grace window are
	// torn down — a genuinely-active concurrent session retains its
	// quota slot and the new init returns 409 below.
	h.supersedeAbandonedArchives(r.Context(), userID)

	// Per-user concurrent quota.
	activeCount, err := h.store.CountActiveLinkArchivesForUser(r.Context(), userID)
	if err != nil {
		slog.Error("link-archive-init: count active", "err", err, "user_id", userID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not check quota"})
		return
	}
	if activeCount >= archiveQuotaForUser() {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "concurrent archive quota exhausted; finish or abort an existing archive before starting a new one",
		})
		return
	}

	// Per-instance staging-bytes ceiling.
	currentStaging, err := h.store.SumActiveLinkArchiveBytes(r.Context())
	if err != nil {
		slog.Error("link-archive-init: sum active bytes", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not check staging cap"})
		return
	}
	if currentStaging+req.TotalBytes > instanceStagingBytesCap() {
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "instance staging-bytes ceiling reached; retry shortly",
		})
		return
	}

	uploadToken, uploadHash, err := newBearerToken()
	if err != nil {
		slog.Error("link-archive-init: rng", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not allocate archive"})
		return
	}
	downloadToken, downloadHash, err := newBearerToken()
	if err != nil {
		slog.Error("link-archive-init: rng", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not allocate archive"})
		return
	}

	now := time.Now().UTC()
	archive, err := h.store.InsertLinkArchive(r.Context(), db.LinkArchiveInsert{
		UserID:            userID,
		UploadTokenHash:   uploadHash,
		DownloadTokenHash: downloadHash,
		TotalChunks:       req.TotalChunks,
		TotalBytes:        req.TotalBytes,
		ChunkSize:         req.ChunkSize,
		ManifestHash:      manifestHash,
		ArchiveSHA256:     archiveSHA256,
		ExpiresAt:         now.Add(linkArchiveDefaultExpiry),
		HardDeadlineAt:    now.Add(linkArchiveMaxLifetime),
	})
	if err != nil {
		slog.Error("link-archive-init: insert", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not allocate archive"})
		return
	}

	// Transition created -> uploading immediately. Failure is non-fatal:
	// the row stays in 'created' and the upload will still write chunks
	// (state is advisory; no endpoint refuses based on 'created' alone).
	if err := h.store.TransitionLinkArchiveState(r.Context(), archive.ID,
		db.LinkArchiveStateUploading, []string{db.LinkArchiveStateCreated}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.Warn("link-archive-init: transition created->uploading", "err", err, "archive_id", archive.ID)
	}

	// Mint the first upload window inline so a small archive (<=
	// linkArchiveWindowSize chunks) can complete without an extra
	// round trip. Larger archives request additional windows from
	// the dedicated endpoint.
	windowEnd := archive.TotalChunks
	if windowEnd > linkArchiveWindowSize {
		windowEnd = linkArchiveWindowSize
	}
	uploadWindow, windowErr := h.mintUploadWindow(r.Context(), archive, 0, windowEnd)
	if windowErr != nil {
		slog.Error("link-archive-init: mint upload window", "err", windowErr, "archive_id", archive.ID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not mint upload window"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"archiveId":      archive.ID,
		"uploadToken":    uploadToken,
		"downloadToken":  downloadToken,
		"backendKind":    string(h.backend.Kind()),
		"uploadWindow":   uploadWindow,
		"expiresAt":      archive.ExpiresAt.Format(time.RFC3339),
		"hardDeadlineAt": archive.HardDeadlineAt.Format(time.RFC3339),
	})
}

// ---------- Upload window ----------

// linkArchiveUploadWindow handles POST /link-archive-upload-window/{archiveId}.
// The OLD device requests presigned PUT URLs for chunk indices [from, to).
// Window size is capped at linkArchiveWindowSize so the manifest payload
// stays small.
func (h *deviceHandler) linkArchiveUploadWindow(w http.ResponseWriter, r *http.Request) {
	archiveID := chi.URLParam(r, "archiveId")
	tokenHash, ok := requireTokenHash(w, r, linkArchiveUploadTokenHeader)
	if !ok {
		return
	}
	archive, ok := h.lookupArchiveByUploadHash(w, r, archiveID, tokenHash)
	if !ok {
		return
	}
	if archive.Finalized {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "archive already finalized"})
		return
	}

	from, to, ok := decodeWindowRange(w, r, archive.TotalChunks)
	if !ok {
		return
	}

	window, err := h.mintUploadWindow(r.Context(), archive, from, to)
	if err != nil {
		slog.Error("link-archive-upload-window: mint", "err", err, "archive_id", archive.ID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not mint upload window"})
		return
	}
	h.refreshArchive(r.Context(), archiveID)
	writeJSON(w, http.StatusOK, window)
}

// linkArchiveDownloadWindow handles POST /link-archive-download-window/{archiveId}.
func (h *deviceHandler) linkArchiveDownloadWindow(w http.ResponseWriter, r *http.Request) {
	archiveID := chi.URLParam(r, "archiveId")
	tokenHash, ok := requireTokenHash(w, r, linkArchiveDownloadHeader)
	if !ok {
		return
	}
	archive, ok := h.lookupArchiveByDownloadHash(w, r, archiveID, tokenHash)
	if !ok {
		return
	}
	if !archive.Finalized {
		writeJSON(w, http.StatusTooEarly, map[string]string{"error": "archive not finalized"})
		return
	}

	from, to, ok := decodeWindowRange(w, r, archive.TotalChunks)
	if !ok {
		return
	}

	window, err := h.mintDownloadWindow(r.Context(), archive, from, to)
	if err != nil {
		slog.Error("link-archive-download-window: mint", "err", err, "archive_id", archive.ID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not mint download window"})
		return
	}
	h.refreshArchive(r.Context(), archiveID)
	writeJSON(w, http.StatusOK, window)
}

// decodeWindowRange parses the {from, to} body and validates it
// against the archive's chunk count and the window size cap.
func decodeWindowRange(w http.ResponseWriter, r *http.Request, totalChunks int) (int, int, bool) {
	var req struct {
		From int `json:"from"`
		To   int `json:"to"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 256)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return 0, 0, false
	}
	if req.From < 0 || req.To <= req.From || req.To > totalChunks {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from/to out of range"})
		return 0, 0, false
	}
	if req.To-req.From > linkArchiveWindowSize {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "window size exceeds server cap"})
		return 0, 0, false
	}
	return req.From, req.To, true
}

// mintUploadWindow returns a presigned-URL window covering chunks
// [from, to). For postgres_bytea backend the URLs point at the in-API
// PUT endpoint; for s3 they are real presigned URLs.
func (h *deviceHandler) mintUploadWindow(ctx context.Context, archive *db.LinkArchive, from, to int) (map[string]any, error) {
	urls := make([]map[string]any, 0, to-from)
	for idx := from; idx < to; idx++ {
		key := fmt.Sprintf("%s/%d", archive.ID, idx)
		ps, err := h.backend.PresignPut(ctx, key, linkArchivePresignTTL)
		if err != nil {
			return nil, err
		}
		urls = append(urls, map[string]any{
			"idx":                 idx,
			"url":                 ps.URL,
			"method":              ps.Method,
			"headers":             ps.Headers,
			"contentSha256Header": ps.ContentSha256Header,
		})
	}
	return map[string]any{
		"from":       from,
		"to":         to,
		"ttlSeconds": int(linkArchivePresignTTL.Seconds()),
		"urls":       urls,
	}, nil
}

// mintDownloadWindow is the symmetrical NEW-device side.
func (h *deviceHandler) mintDownloadWindow(ctx context.Context, archive *db.LinkArchive, from, to int) (map[string]any, error) {
	urls := make([]map[string]any, 0, to-from)
	for idx := from; idx < to; idx++ {
		key := fmt.Sprintf("%s/%d", archive.ID, idx)
		ps, err := h.backend.PresignGet(ctx, key, linkArchivePresignTTL)
		if err != nil {
			return nil, err
		}
		urls = append(urls, map[string]any{
			"idx":     idx,
			"url":     ps.URL,
			"method":  ps.Method,
			"headers": ps.Headers,
		})
	}
	return map[string]any{
		"from":       from,
		"to":         to,
		"ttlSeconds": int(linkArchivePresignTTL.Seconds()),
		"urls":       urls,
	}, nil
}

// ---------- Confirm chunk ----------

// linkArchiveConfirmChunk handles POST /link-archive-confirm-chunk/{archiveId}/{idx}.
//
// After the OLD device has PUT a chunk to the storage backend (either
// directly via a presigned URL for s3 or via the in-API endpoint for
// postgres_bytea), it calls this endpoint with the SHA-256 it
// computed locally. The server verifies that hash against the
// authoritative one stored alongside the object (S3 native checksum
// or the postgres_bytea backend's own digest), then writes the chunk
// row. ETag is never consulted.
//
// For the postgres_bytea backend, the in-API upload handler already
// writes the chunk row inline; this endpoint is a no-op accept for
// that backend so the client can use the same flow uniformly. The
// server tells the client which path to take via the `backendKind`
// field in the init response.
func (h *deviceHandler) linkArchiveConfirmChunk(w http.ResponseWriter, r *http.Request) {
	archiveID := chi.URLParam(r, "archiveId")
	idx, err := strconv.Atoi(chi.URLParam(r, "idx"))
	if err != nil || idx < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid idx"})
		return
	}

	tokenHash, ok := requireTokenHash(w, r, linkArchiveUploadTokenHeader)
	if !ok {
		return
	}
	archive, ok := h.lookupArchiveByUploadHash(w, r, archiveID, tokenHash)
	if !ok {
		return
	}
	if archive.Finalized {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "archive already finalized"})
		return
	}
	if idx >= archive.TotalChunks {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "idx out of range"})
		return
	}

	var req struct {
		ChunkSha256 string `json:"chunkSha256"`
		ChunkSize   int    `json:"chunkSize"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 512)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	declaredHash, err := decodeHashB64(req.ChunkSha256)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chunkSha256 must be base64 sha256"})
		return
	}
	if req.ChunkSize <= 0 || req.ChunkSize > linkArchiveChunkBodyMax {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chunkSize out of range"})
		return
	}

	storageKey := fmt.Sprintf("%s/%d", archiveID, idx)

	// For s3, verify the backend-stored SHA-256 matches the
	// client-declared value before persisting the chunk row.
	if s3, isS3 := h.backend.(*storage.S3Backend); isS3 {
		stored, sErr := s3.StatChecksumSHA256(r.Context(), storageKey)
		if sErr != nil {
			if errors.Is(sErr, storage.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "chunk not found in storage"})
				return
			}
			slog.Error("link-archive-confirm-chunk: stat", "err", sErr, "archive_id", archiveID, "idx", idx)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not verify chunk"})
			return
		}
		if len(stored) == 0 {
			// Bucket configured without checksum support, or client did
			// not include the header. Reject loudly: the design forbids
			// trusting ETag.
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error": "chunk uploaded without x-amz-checksum-sha256; reupload with the checksum header",
			})
			return
		}
		if !slicesEqualConstantTime(stored, declaredHash) {
			// Chunk on disk does not match what the client declared.
			// Roll back the storage object before failing.
			_ = h.backend.Delete(r.Context(), storageKey)
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"error": "stored checksum does not match declared hash",
			})
			return
		}
	} else {
		// postgres_bytea path: the chunk row was written inline by the
		// in-API PUT handler; confirm-chunk for this backend is a
		// no-op accept and just refreshes expiry. We still gate on
		// chunk presence so a misbehaving client cannot pretend a
		// chunk landed when it did not.
		if exists, eErr := h.backend.Exists(r.Context(), storageKey); eErr != nil || !exists {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "chunk not found in storage"})
			return
		}
		h.refreshArchive(r.Context(), archiveID)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if insertErr := h.store.InsertLinkArchiveChunk(r.Context(), db.LinkArchiveChunkInsert{
		ArchiveID:      archiveID,
		Idx:            idx,
		ChunkSize:      req.ChunkSize,
		ChunkHash:      declaredHash,
		StorageBackend: string(h.backend.Kind()),
		StorageKey:     storageKey,
	}); insertErr != nil {
		if errors.Is(insertErr, db.ErrLinkArchiveChunkConflict) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "chunk hash conflict for index"})
			return
		}
		slog.Error("link-archive-confirm-chunk: insert", "err", insertErr, "archive_id", archiveID, "idx", idx)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not confirm chunk"})
		return
	}

	h.refreshArchive(r.Context(), archiveID)
	w.WriteHeader(http.StatusNoContent)
}

// ---------- Upload chunk ----------

// linkArchiveUploadChunk handles PUT /link-archive-chunk/{archiveId}/{idx}.
// Streams the body into the configured storage backend via Put, verifies
// the SHA-256 the server itself computed against the X-Chunk-Sha256
// header, and writes the chunk row referencing the storage backend +
// storage key.
func (h *deviceHandler) linkArchiveUploadChunk(w http.ResponseWriter, r *http.Request) {
	archiveID := chi.URLParam(r, "archiveId")
	idx, err := strconv.Atoi(chi.URLParam(r, "idx"))
	if err != nil || idx < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid idx"})
		return
	}

	tokenHash, ok := requireTokenHash(w, r, linkArchiveUploadTokenHeader)
	if !ok {
		return
	}
	archive, ok := h.lookupArchiveByUploadHash(w, r, archiveID, tokenHash)
	if !ok {
		return
	}
	if archive.Finalized {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "archive already finalized"})
		return
	}
	if idx >= archive.TotalChunks {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "idx out of range"})
		return
	}

	declaredHash, err := decodeHashB64(r.Header.Get(linkArchiveChunkHashHeader))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "X-Chunk-Sha256 header is required (base64 sha256)"})
		return
	}

	body := http.MaxBytesReader(w, r.Body, int64(linkArchiveChunkBodyMax))
	defer body.Close()

	// Materialise the body so we can both Put it and probe its size.
	bytesBuf, err := io.ReadAll(body)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "chunk exceeds maximum size"})
		return
	}
	if len(bytesBuf) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chunk body is empty"})
		return
	}

	// Backend computes its own SHA-256; we use that for the integrity
	// check rather than trusting the wire-side hash blindly. The header
	// is verified against the backend's hash; mismatch = 400.
	storageKey := fmt.Sprintf("%s/%d", archiveID, idx)
	put, err := h.backend.Put(r.Context(), storageKey, asReader(bytesBuf), int64(len(bytesBuf)))
	if err != nil {
		slog.Error("link-archive: backend put", "err", err, "archive_id", archiveID, "idx", idx)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not store chunk"})
		return
	}
	if !slicesEqualConstantTime(put.Sha256, declaredHash) {
		// Backend persisted the bytes but the client lied about the hash.
		// Roll back the backend write before failing.
		_ = h.backend.Delete(r.Context(), storageKey)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body hash does not match X-Chunk-Sha256"})
		return
	}

	if insertErr := h.store.InsertLinkArchiveChunk(r.Context(), db.LinkArchiveChunkInsert{
		ArchiveID:      archiveID,
		Idx:            idx,
		ChunkSize:      len(bytesBuf),
		ChunkHash:      put.Sha256,
		StorageBackend: string(h.backend.Kind()),
		StorageKey:     storageKey,
	}); insertErr != nil {
		if errors.Is(insertErr, db.ErrLinkArchiveChunkConflict) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "chunk hash conflict for index"})
			return
		}
		slog.Error("link-archive: insert chunk", "err", insertErr, "archive_id", archiveID, "idx", idx)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not store chunk"})
		return
	}

	h.refreshArchive(r.Context(), archiveID)
	w.WriteHeader(http.StatusNoContent)
}

// ---------- Finalize ----------

// linkArchiveFinalize handles POST /link-archive-finalize/{archiveId}.
// Verifies all chunks are present, manifest hash matches, and total
// bytes match; transitions state uploading -> uploaded -> available.
func (h *deviceHandler) linkArchiveFinalize(w http.ResponseWriter, r *http.Request) {
	archiveID := chi.URLParam(r, "archiveId")

	tokenHash, ok := requireTokenHash(w, r, linkArchiveUploadTokenHeader)
	if !ok {
		return
	}
	archive, ok := h.lookupArchiveByUploadHash(w, r, archiveID, tokenHash)
	if !ok {
		return
	}
	if archive.Finalized {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	chunks, err := h.store.ListLinkArchiveChunkRows(r.Context(), archiveID)
	if err != nil {
		slog.Error("link-archive: list chunks", "err", err, "archive_id", archiveID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not enumerate chunks"})
		return
	}

	present := make(map[int]db.LinkArchiveChunkRow, len(chunks))
	for _, c := range chunks {
		present[c.Idx] = c
	}
	missing := make([]int, 0)
	for i := 0; i < archive.TotalChunks; i++ {
		if _, ok := present[i]; !ok {
			missing = append(missing, i)
		}
	}
	if len(missing) > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":   "archive incomplete",
			"missing": missing,
		})
		return
	}

	totalBytes := int64(0)
	manifest := sha256.New()
	for i := 0; i < archive.TotalChunks; i++ {
		row := present[i]
		manifest.Write(row.ChunkHash)
		totalBytes += int64(row.ChunkSize)
	}
	manifestHash := manifest.Sum(nil)
	if !slicesEqualConstantTime(manifestHash, archive.ManifestHash) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "manifest hash mismatch"})
		return
	}
	if totalBytes != archive.TotalBytes {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "total bytes mismatch"})
		return
	}

	if err := h.store.MarkLinkArchiveFinalized(r.Context(), archiveID); err != nil {
		slog.Error("link-archive: mark finalized", "err", err, "archive_id", archiveID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not finalize archive"})
		return
	}
	// uploading -> uploaded -> available. Both transitions are advisory:
	// Finalized=true is the authoritative gate for downloads.
	_ = h.store.TransitionLinkArchiveState(r.Context(), archiveID,
		db.LinkArchiveStateUploaded, []string{db.LinkArchiveStateUploading, db.LinkArchiveStateUploadPaused})
	_ = h.store.TransitionLinkArchiveState(r.Context(), archiveID,
		db.LinkArchiveStateAvailable, []string{db.LinkArchiveStateUploaded})

	h.refreshArchive(r.Context(), archiveID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------- Manifest ----------

// linkArchiveManifest handles GET /link-archive-manifest/{archiveId}.
func (h *deviceHandler) linkArchiveManifest(w http.ResponseWriter, r *http.Request) {
	archiveID := chi.URLParam(r, "archiveId")

	tokenHash, ok := requireTokenHash(w, r, linkArchiveDownloadHeader)
	if !ok {
		return
	}
	archive, ok := h.lookupArchiveByDownloadHash(w, r, archiveID, tokenHash)
	if !ok {
		return
	}
	if !archive.Finalized {
		writeJSON(w, http.StatusTooEarly, map[string]string{"error": "archive not finalized"})
		return
	}

	chunks, err := h.store.ListLinkArchiveChunkRows(r.Context(), archiveID)
	if err != nil {
		slog.Error("link-archive: list chunks for manifest", "err", err, "archive_id", archiveID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read manifest"})
		return
	}

	hashes := make([]string, archive.TotalChunks)
	for _, c := range chunks {
		hashes[c.Idx] = base64.StdEncoding.EncodeToString(c.ChunkHash)
	}
	h.refreshArchive(r.Context(), archiveID)

	writeJSON(w, http.StatusOK, map[string]any{
		"totalChunks":   archive.TotalChunks,
		"chunkSize":     archive.ChunkSize,
		"totalBytes":    archive.TotalBytes,
		"manifestHash":  base64.StdEncoding.EncodeToString(archive.ManifestHash),
		"archiveSha256": base64.StdEncoding.EncodeToString(archive.ArchiveSHA256),
		"chunkHashes":   hashes,
		"expiresAt":     archive.ExpiresAt.Format(time.RFC3339),
	})
}

// ---------- Download chunk ----------

// linkArchiveDownloadChunk handles GET /link-archive-chunk/{archiveId}/{idx}.
// Resolves the chunk row's storage_backend + storage_key, then routes
// the body through the corresponding storage backend's Get.
func (h *deviceHandler) linkArchiveDownloadChunk(w http.ResponseWriter, r *http.Request) {
	archiveID := chi.URLParam(r, "archiveId")
	idx, err := strconv.Atoi(chi.URLParam(r, "idx"))
	if err != nil || idx < 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid idx"})
		return
	}

	tokenHash, ok := requireTokenHash(w, r, linkArchiveDownloadHeader)
	if !ok {
		return
	}
	archive, ok := h.lookupArchiveByDownloadHash(w, r, archiveID, tokenHash)
	if !ok {
		return
	}
	if !archive.Finalized {
		writeJSON(w, http.StatusTooEarly, map[string]string{"error": "archive not finalized"})
		return
	}
	if idx >= archive.TotalChunks {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "idx out of range"})
		return
	}

	_, storageKey, err := h.store.GetLinkArchiveChunkPointer(r.Context(), archiveID, idx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "chunk not found"})
			return
		}
		slog.Error("link-archive: get chunk pointer", "err", err, "archive_id", archiveID, "idx", idx)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read chunk metadata"})
		return
	}

	reader, size, err := h.backend.Get(r.Context(), storageKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "chunk not found"})
			return
		}
		slog.Error("link-archive: backend get", "err", err, "archive_id", archiveID, "idx", idx)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not read chunk"})
		return
	}
	defer reader.Close()

	h.refreshArchive(r.Context(), archiveID)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	if _, err := io.Copy(w, reader); err != nil {
		slog.Warn("link-archive: write chunk body", "err", err, "archive_id", archiveID, "idx", idx)
	}
}

// ---------- Ack ----------

// linkArchiveAck handles POST /link-archive-ack/{archiveId}. The NEW
// device declares import complete; the archive transitions to
// 'acknowledged' and becomes GC-eligible on the next purger tick.
// Authorisation: the download capability token (only the NEW device
// has it).
func (h *deviceHandler) linkArchiveAck(w http.ResponseWriter, r *http.Request) {
	archiveID := chi.URLParam(r, "archiveId")
	tokenHash, ok := requireTokenHash(w, r, linkArchiveDownloadHeader)
	if !ok {
		return
	}
	archive, ok := h.lookupArchiveByDownloadHash(w, r, archiveID, tokenHash)
	if !ok {
		return
	}
	// Allow ack from any post-available state — we do not yet track the
	// 'imported' transition server-side because the import lifecycle on
	// the NEW device has not landed; the ack itself carries the
	// information that the client finished.
	allowedFrom := []string{
		db.LinkArchiveStateAvailable,
		db.LinkArchiveStateImporting,
		db.LinkArchiveStateImportPaused,
		db.LinkArchiveStateImported,
	}
	if err := h.store.TransitionLinkArchiveState(r.Context(), archive.ID,
		db.LinkArchiveStateAcknowledged, allowedFrom); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "archive not in an ack-able state"})
			return
		}
		slog.Error("link-archive: ack transition", "err", err, "archive_id", archive.ID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not ack archive"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------- Delete ----------

// linkArchiveDelete handles DELETE /link-archive/{archiveId}. Either
// the upload or the download token authorises the abort. Storage
// backend objects are deleted before the DB row.
func (h *deviceHandler) linkArchiveDelete(w http.ResponseWriter, r *http.Request) {
	archiveID := chi.URLParam(r, "archiveId")

	var uploadHash, downloadHash []byte
	if v := strings.TrimSpace(r.Header.Get(linkArchiveUploadTokenHeader)); v != "" {
		sum := sha256.Sum256([]byte(v))
		uploadHash = sum[:]
	}
	if v := strings.TrimSpace(r.Header.Get(linkArchiveDownloadHeader)); v != "" {
		sum := sha256.Sum256([]byte(v))
		downloadHash = sum[:]
	}
	if len(uploadHash) == 0 && len(downloadHash) == 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "upload or download token required"})
		return
	}

	if uploadHash != nil {
		archive, err := h.store.GetLinkArchiveByUploadTokenHash(r.Context(), archiveID, uploadHash)
		if err == nil && archive != nil {
			h.cleanupAndDeleteArchive(w, r, archiveID)
			return
		}
	}
	if downloadHash != nil {
		archive, err := h.store.GetLinkArchiveByDownloadTokenHash(r.Context(), archiveID, downloadHash)
		if err == nil && archive != nil {
			h.cleanupAndDeleteArchive(w, r, archiveID)
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "archive not found"})
}

// ---------- Supervised purger ----------

// purgeLinkArchivesSupervised reaps GC-eligible archives on a fixed
// tick. Honours ctx.Done() for structured shutdown so the goroutine
// exits cleanly when the API process receives SIGTERM. Each purger
// tick deletes storage-backend objects before the DB row so a crash
// between the two leaves orphan objects rather than orphan DB rows
// (orphan objects are detectable via Exists; orphan DB rows would
// silently fail subsequent downloads).
func (h *deviceHandler) purgeLinkArchivesSupervised(ctx context.Context) {
	ticker := time.NewTicker(linkArchivePurgeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Info("link-archive purger: shutdown")
			return
		case <-ticker.C:
			h.runPurgerTick(ctx)
		}
	}
}

// runPurgerTick lists GC-eligible archives, walks their chunks, deletes
// each from the storage backend, then deletes the DB row.
func (h *deviceHandler) runPurgerTick(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, linkArchivePurgeContextSecs*time.Second)
	defer cancel()

	ids, err := h.store.ListGcEligibleLinkArchives(ctx, linkArchivePurgeBatchLimit)
	if err != nil {
		slog.Error("link-archive purger: list", "err", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	for _, id := range ids {
		// Best-effort backend cleanup. We list the chunk rows and delete
		// each storage object before the DB row.
		chunks, err := h.store.ListLinkArchiveChunkRows(ctx, id)
		if err != nil {
			slog.Warn("link-archive purger: list chunks", "err", err, "archive_id", id)
		} else {
			for _, c := range chunks {
				if err := h.backend.Delete(ctx, c.StorageKey); err != nil {
					slog.Warn("link-archive purger: backend delete",
						"err", err, "archive_id", id, "idx", c.Idx, "storage_key", c.StorageKey)
				}
			}
		}
		if err := h.store.DeleteLinkArchive(ctx, id); err != nil {
			slog.Warn("link-archive purger: db delete", "err", err, "archive_id", id)
			continue
		}
	}
	slog.Info("link-archive purger: gc complete", "archives_deleted", len(ids))
}

// ---------- Helpers ----------

// cleanupAndDeleteArchive runs the same backend-then-DB cleanup the
// purger uses, but on demand for an explicit DELETE call.
func (h *deviceHandler) cleanupAndDeleteArchive(w http.ResponseWriter, r *http.Request, archiveID string) {
	if err := h.purgeArchive(r.Context(), archiveID, "user-initiated DELETE"); err != nil {
		slog.Error("link-archive: delete", "err", err, "archive_id", archiveID)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not delete archive"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// purgeArchive deletes the archive's storage-backend objects, then the DB
// row. Returns the DB delete error so the caller can surface a non-2xx
// HTTP response when the explicit DELETE path runs; backend object delete
// failures are logged but do not abort — the supervised purger will
// retry orphans on its next tick.
//
// reason is included in the warning logs so operators can distinguish
// the supersede path from the explicit user-DELETE path.
func (h *deviceHandler) purgeArchive(ctx context.Context, archiveID, reason string) error {
	chunks, err := h.store.ListLinkArchiveChunkRows(ctx, archiveID)
	if err == nil {
		for _, c := range chunks {
			if delErr := h.backend.Delete(ctx, c.StorageKey); delErr != nil {
				slog.Warn("link-archive: backend delete",
					"err", delErr, "archive_id", archiveID, "idx", c.Idx, "reason", reason)
			}
		}
	}
	return h.store.DeleteLinkArchive(ctx, archiveID)
}

// supersedeAbandonedArchives walks the user's prior active archives and
// tears down any that have not seen a sliding-expiry refresh within the
// configured grace window. Failures are logged; the caller continues
// because an honest quota check below will still gate the new
// allocation if supersede did not free a slot.
func (h *deviceHandler) supersedeAbandonedArchives(ctx context.Context, userID string) {
	threshold := time.Now().UTC().Add(linkArchiveDefaultExpiry - archiveSupersedeGrace())
	staleIDs, err := h.store.ListSupersedableLinkArchivesForUser(ctx, userID, threshold)
	if err != nil {
		slog.Warn("link-archive-init: list supersedable", "err", err, "user_id", userID)
		return
	}
	for _, id := range staleIDs {
		slog.Info("link-archive-init: superseding abandoned prior archive",
			"user_id", userID, "archive_id", id)
		if err := h.purgeArchive(ctx, id, "auto-supersede"); err != nil {
			slog.Warn("link-archive-init: purge supersede", "err", err, "archive_id", id)
		}
	}
}

// newBearerToken generates a 32-byte random URL-safe base64 token and returns
// the raw token together with its SHA-256 digest. Only the digest is stored.
func newBearerToken() (raw string, digest []byte, err error) {
	bytes := make([]byte, linkArchiveTokenBytes)
	if _, err := rand.Read(bytes); err != nil {
		return "", nil, err
	}
	raw = base64.RawURLEncoding.EncodeToString(bytes)
	hash := sha256.Sum256([]byte(raw))
	return raw, hash[:], nil
}

// requireTokenHash extracts and SHA-256-hashes the bearer token from the
// requested header. Writes a 401 response and returns ok=false on any failure.
func requireTokenHash(w http.ResponseWriter, r *http.Request, header string) ([]byte, bool) {
	raw := strings.TrimSpace(r.Header.Get(header))
	if raw == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing " + header})
		return nil, false
	}
	hash := sha256.Sum256([]byte(raw))
	return hash[:], true
}

// lookupArchiveByUploadHash returns the archive matching id+upload hash or
// writes a 404 / expired response and returns ok=false.
func (h *deviceHandler) lookupArchiveByUploadHash(w http.ResponseWriter, r *http.Request, archiveID string, tokenHash []byte) (*db.LinkArchive, bool) {
	archive, err := h.store.GetLinkArchiveByUploadTokenHash(r.Context(), archiveID, tokenHash)
	return archiveOrError(w, archive, err)
}

func (h *deviceHandler) lookupArchiveByDownloadHash(w http.ResponseWriter, r *http.Request, archiveID string, tokenHash []byte) (*db.LinkArchive, bool) {
	archive, err := h.store.GetLinkArchiveByDownloadTokenHash(r.Context(), archiveID, tokenHash)
	return archiveOrError(w, archive, err)
}

func archiveOrError(w http.ResponseWriter, archive *db.LinkArchive, err error) (*db.LinkArchive, bool) {
	if err == nil && archive != nil {
		return archive, true
	}
	if errors.Is(err, db.ErrLinkArchiveExpired) {
		writeJSON(w, http.StatusGone, map[string]string{"error": "archive expired"})
		return nil, false
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "archive not found"})
		return nil, false
	}
	if err != nil {
		slog.Error("link-archive: lookup", "err", err)
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "archive not found"})
	return nil, false
}

// refreshArchive bumps the sliding expires_at; failures are logged and ignored
// — they do not affect the in-flight request which is otherwise successful.
func (h *deviceHandler) refreshArchive(ctx context.Context, archiveID string) {
	if _, err := h.store.RefreshLinkArchiveExpiry(ctx, archiveID, linkArchiveDefaultExpiry); err != nil {
		slog.Warn("link-archive: refresh expiry", "err", err, "archive_id", archiveID)
	}
}

// decodeHashB64 accepts either standard or URL base64 sha256 strings; rejects
// anything that does not decode to exactly 32 bytes.
func decodeHashB64(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("empty hash")
	}
	for _, dec := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if raw, err := dec.DecodeString(value); err == nil {
			if len(raw) != sha256.Size {
				continue
			}
			return raw, nil
		}
	}
	return nil, errors.New("invalid sha256 hash")
}

// slicesEqualConstantTime is a length-aware constant-time byte comparison.
func slicesEqualConstantTime(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// byteReader is a tiny io.Reader over a byte slice. Used to feed an
// already-buffered request body through the storage backend's
// streaming Put interface without pulling in bytes.NewReader (which is
// also fine but this stays close to the existing style and avoids
// allocating an extra struct).
type byteReader []byte

func (b *byteReader) Read(p []byte) (int, error) {
	if len(*b) == 0 {
		return 0, io.EOF
	}
	n := copy(p, *b)
	*b = (*b)[n:]
	return n, nil
}

// asReader converts a []byte into a single-shot reader.
func asReader(b []byte) io.Reader {
	br := byteReader(b)
	return &br
}
