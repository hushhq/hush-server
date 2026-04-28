package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hushhq/hush-server/internal/db"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLinkArchiveStore is an in-memory store satisfying the link
// archive subset of db.Store (including the chunk-blob plane consumed
// by the postgres_bytea storage backend). Tests touch only the link-
// archive function pointers; everything else inherits zero behaviour
// from the base mockStore.
type fakeLinkArchiveStore struct {
	mu       *mockStore
	archives map[string]*db.LinkArchive
	chunks   map[string]map[int]storedChunk
	blobs    map[string][]byte
}

type storedChunk struct {
	bytes      []byte
	hash       []byte
	size       int
	storageKey string
	backend    string
}

func newFakeLinkArchiveStore() *fakeLinkArchiveStore {
	s := &fakeLinkArchiveStore{
		mu:       &mockStore{},
		archives: make(map[string]*db.LinkArchive),
		chunks:   make(map[string]map[int]storedChunk),
		blobs:    make(map[string][]byte),
	}

	s.mu.insertLinkArchiveFn = func(_ context.Context, in db.LinkArchiveInsert) (*db.LinkArchive, error) {
		row := &db.LinkArchive{
			ID:                uuid.NewString(),
			UserID:            in.UserID,
			UploadTokenHash:   append([]byte(nil), in.UploadTokenHash...),
			DownloadTokenHash: append([]byte(nil), in.DownloadTokenHash...),
			TotalChunks:       in.TotalChunks,
			TotalBytes:        in.TotalBytes,
			ChunkSize:         in.ChunkSize,
			ManifestHash:      append([]byte(nil), in.ManifestHash...),
			ArchiveSHA256:     append([]byte(nil), in.ArchiveSHA256...),
			Finalized:         false,
			State:             db.LinkArchiveStateCreated,
			CreatedAt:         time.Now().UTC(),
			ExpiresAt:         in.ExpiresAt,
			HardDeadlineAt:    in.HardDeadlineAt,
		}
		s.archives[row.ID] = row
		s.chunks[row.ID] = map[int]storedChunk{}
		return row, nil
	}
	s.mu.countActiveLinkArchivesForUserFn = func(_ context.Context, userID string) (int, error) {
		n := 0
		for _, a := range s.archives {
			if a.UserID == userID && isActiveState(a.State) {
				n++
			}
		}
		return n, nil
	}
	s.mu.listSupersedableLinkArchivesForUserFn = func(_ context.Context, userID string, lastTouchBefore time.Time) ([]string, error) {
		out := make([]string, 0)
		for _, a := range s.archives {
			if a.UserID == userID && isActiveState(a.State) && a.ExpiresAt.Before(lastTouchBefore) {
				out = append(out, a.ID)
			}
		}
		return out, nil
	}
	s.mu.sumActiveLinkArchiveBytesFn = func(_ context.Context) (int64, error) {
		var total int64
		for _, a := range s.archives {
			if isActiveState(a.State) {
				total += a.TotalBytes
			}
		}
		return total, nil
	}
	s.mu.transitionLinkArchiveStateFn = func(_ context.Context, archiveID, next string, allowed []string) error {
		row, ok := s.archives[archiveID]
		if !ok {
			return pgx.ErrNoRows
		}
		for _, st := range allowed {
			if row.State == st {
				row.State = next
				return nil
			}
		}
		return pgx.ErrNoRows
	}
	freshOrExpired := func(row *db.LinkArchive) (*db.LinkArchive, error) {
		now := time.Now().UTC()
		if !row.ExpiresAt.After(now) || !row.HardDeadlineAt.After(now) {
			return nil, db.ErrLinkArchiveExpired
		}
		return row, nil
	}
	s.mu.getLinkArchiveByIDFn = func(_ context.Context, id string) (*db.LinkArchive, error) {
		row, ok := s.archives[id]
		if !ok {
			return nil, pgx.ErrNoRows
		}
		return freshOrExpired(row)
	}
	s.mu.getLinkArchiveByUploadTokenHashFn = func(_ context.Context, id string, hash []byte) (*db.LinkArchive, error) {
		row, ok := s.archives[id]
		if !ok || !bytes.Equal(row.UploadTokenHash, hash) {
			return nil, pgx.ErrNoRows
		}
		return freshOrExpired(row)
	}
	s.mu.getLinkArchiveByDownloadTokenHashFn = func(_ context.Context, id string, hash []byte) (*db.LinkArchive, error) {
		row, ok := s.archives[id]
		if !ok || !bytes.Equal(row.DownloadTokenHash, hash) {
			return nil, pgx.ErrNoRows
		}
		return freshOrExpired(row)
	}
	s.mu.refreshLinkArchiveExpiryFn = func(_ context.Context, id string, ttl time.Duration) (time.Time, error) {
		row, ok := s.archives[id]
		if !ok {
			return time.Time{}, pgx.ErrNoRows
		}
		next := time.Now().Add(ttl)
		if next.After(row.HardDeadlineAt) {
			next = row.HardDeadlineAt
		}
		row.ExpiresAt = next
		return next, nil
	}
	s.mu.insertLinkArchiveChunkFn = func(_ context.Context, in db.LinkArchiveChunkInsert) error {
		store := s.chunks[in.ArchiveID]
		if existing, ok := store[in.Idx]; ok {
			if !bytes.Equal(existing.hash, in.ChunkHash) {
				return db.ErrLinkArchiveChunkConflict
			}
			return nil
		}
		store[in.Idx] = storedChunk{
			hash:       append([]byte(nil), in.ChunkHash...),
			size:       in.ChunkSize,
			storageKey: in.StorageKey,
			backend:    in.StorageBackend,
		}
		return nil
	}
	s.mu.getLinkArchiveChunkPointerFn = func(_ context.Context, id string, idx int) (string, string, error) {
		c, ok := s.chunks[id][idx]
		if !ok {
			return "", "", pgx.ErrNoRows
		}
		return c.backend, c.storageKey, nil
	}
	s.mu.listLinkArchiveChunkRowsFn = func(_ context.Context, id string) ([]db.LinkArchiveChunkRow, error) {
		out := make([]db.LinkArchiveChunkRow, 0, len(s.chunks[id]))
		for idx, c := range s.chunks[id] {
			out = append(out, db.LinkArchiveChunkRow{
				Idx:            idx,
				ChunkSize:      c.size,
				ChunkHash:      append([]byte(nil), c.hash...),
				StorageBackend: c.backend,
				StorageKey:     c.storageKey,
			})
		}
		return out, nil
	}
	s.mu.markLinkArchiveFinalizedFn = func(_ context.Context, id string) error {
		row, ok := s.archives[id]
		if !ok {
			return pgx.ErrNoRows
		}
		row.Finalized = true
		return nil
	}
	s.mu.deleteLinkArchiveFn = func(_ context.Context, id string) error {
		delete(s.archives, id)
		delete(s.chunks, id)
		return nil
	}
	s.mu.listGcEligibleLinkArchivesFn = func(_ context.Context, _ int) ([]string, error) {
		out := make([]string, 0)
		now := time.Now().UTC()
		for _, a := range s.archives {
			switch a.State {
			case db.LinkArchiveStateAcknowledged, db.LinkArchiveStateAborted:
				out = append(out, a.ID)
			case db.LinkArchiveStateTerminalFailure, db.LinkArchiveStateImported:
				if a.ExpiresAt.Before(now.Add(-24 * time.Hour)) {
					out = append(out, a.ID)
				}
			default:
				if !a.ExpiresAt.After(now) || !a.HardDeadlineAt.After(now) {
					out = append(out, a.ID)
				}
			}
		}
		return out, nil
	}

	// Chunk-blob plane (postgres_bytea backend).
	s.mu.upsertChunkBlobFn = func(_ context.Context, key string, body []byte) error {
		s.blobs[key] = append([]byte(nil), body...)
		return nil
	}
	s.mu.getChunkBlobFn = func(_ context.Context, key string) ([]byte, error) {
		raw, ok := s.blobs[key]
		if !ok {
			return nil, pgx.ErrNoRows
		}
		return raw, nil
	}
	s.mu.deleteChunkBlobFn = func(_ context.Context, key string) error {
		delete(s.blobs, key)
		return nil
	}
	s.mu.chunkBlobExistsFn = func(_ context.Context, key string) (bool, error) {
		_, ok := s.blobs[key]
		return ok, nil
	}

	return s
}

func isActiveState(state string) bool {
	for _, s := range db.LinkArchiveActiveStates {
		if s == state {
			return true
		}
	}
	return false
}

// helpers ----------------------------------------------------------------

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func sha256Sum(b []byte) []byte {
	h := sha256.Sum256(b)
	return h[:]
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }

// uploadAndFinalize performs an init+upload+finalize cycle and returns
// the archive id, upload token, download token, manifestHash, and
// archiveSha256 for downstream assertions.
func uploadAndFinalize(t *testing.T, store *fakeLinkArchiveStore, token string, slices [][]byte) (string, string, string, []byte, []byte) {
	t.Helper()
	chunkHashes := make([][]byte, len(slices))
	manifest := sha256.New()
	totalBytes := int64(0)
	for i, s := range slices {
		chunkHashes[i] = sha256Sum(s)
		manifest.Write(chunkHashes[i])
		totalBytes += int64(len(s))
	}
	manifestHash := manifest.Sum(nil)
	archiveSha := sha256Sum(bytes.Join(slices, nil))

	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	initRR := postJSONWithAuth(handler, "/link-archive-init", token, map[string]any{
		"totalChunks":   len(slices),
		"totalBytes":    totalBytes,
		"chunkSize":     linkArchiveChunkSize,
		"manifestHash":  b64(manifestHash),
		"archiveSha256": b64(archiveSha),
	})
	require.Equal(t, http.StatusOK, initRR.Code, initRR.Body.String())
	var initResp struct {
		ArchiveID, UploadToken, DownloadToken string
	}
	require.NoError(t, json.Unmarshal(initRR.Body.Bytes(), &initResp))

	for i, s := range slices {
		req := httptest.NewRequest(http.MethodPut,
			"/link-archive-chunk/"+initResp.ArchiveID+"/"+itoa(i),
			bytes.NewReader(s))
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Upload-Token", initResp.UploadToken)
		req.Header.Set("X-Chunk-Sha256", b64(chunkHashes[i]))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	}

	finReq := httptest.NewRequest(http.MethodPost,
		"/link-archive-finalize/"+initResp.ArchiveID, nil)
	finReq.Header.Set("Authorization", "Bearer "+token)
	finReq.Header.Set("X-Upload-Token", initResp.UploadToken)
	finRR := httptest.NewRecorder()
	handler.ServeHTTP(finRR, finReq)
	require.Equal(t, http.StatusOK, finRR.Code, finRR.Body.String())

	return initResp.ArchiveID, initResp.UploadToken, initResp.DownloadToken, manifestHash, archiveSha
}

// tests ------------------------------------------------------------------

func TestLinkArchive_Init_ReturnsBackendKindAndUploadWindow(t *testing.T) {
	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	rr := postJSONWithAuth(handler, "/link-archive-init", token, map[string]any{
		"totalChunks":   3,
		"totalBytes":    int64(64),
		"chunkSize":     linkArchiveChunkSize,
		"manifestHash":  b64(make([]byte, 32)),
		"archiveSha256": b64(make([]byte, 32)),
	})
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp struct {
		ArchiveID    string `json:"archiveId"`
		BackendKind  string `json:"backendKind"`
		UploadWindow struct {
			From       int  `json:"from"`
			To         int  `json:"to"`
			TtlSeconds int  `json:"ttlSeconds"`
			URLs       []struct {
				Idx                 int    `json:"idx"`
				URL                 string `json:"url"`
				Method              string `json:"method"`
				ContentSha256Header string `json:"contentSha256Header"`
			} `json:"urls"`
		} `json:"uploadWindow"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Equal(t, "postgres_bytea", resp.BackendKind)
	require.Equal(t, 0, resp.UploadWindow.From)
	require.Equal(t, 3, resp.UploadWindow.To)
	require.Len(t, resp.UploadWindow.URLs, 3)
	for i, entry := range resp.UploadWindow.URLs {
		require.Equal(t, i, entry.Idx)
		require.Equal(t, "PUT", entry.Method)
		require.Contains(t, entry.URL, "/api/auth/link-archive-chunk/"+resp.ArchiveID+"/"+itoa(i))
	}
}

func TestLinkArchive_UploadWindow_RejectsLargeRange(t *testing.T) {
	t.Setenv("LINK_ARCHIVE_USER_QUOTA", "10")
	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	initRR := postJSONWithAuth(handler, "/link-archive-init", token, map[string]any{
		"totalChunks":   16,
		"totalBytes":    int64(linkArchiveChunkSize) * 16,
		"chunkSize":     linkArchiveChunkSize,
		"manifestHash":  b64(make([]byte, 32)),
		"archiveSha256": b64(make([]byte, 32)),
	})
	require.Equal(t, http.StatusOK, initRR.Code)
	var init struct{ ArchiveID, UploadToken string }
	require.NoError(t, json.Unmarshal(initRR.Body.Bytes(), &init))

	// Window size of 9 (> server cap of 8) is rejected.
	req := httptest.NewRequest(http.MethodPost,
		"/link-archive-upload-window/"+init.ArchiveID,
		bytes.NewReader([]byte(`{"from":0,"to":9}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Upload-Token", init.UploadToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
}

func TestLinkArchive_ConfirmChunk_PostgresByteaIsNoOp(t *testing.T) {
	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	slice := bytes.Repeat([]byte{0x55}, 16)
	chunkHash := sha256Sum(slice)
	manifestHash := sha256Sum(chunkHash)
	archiveSha := sha256Sum(slice)
	initRR := postJSONWithAuth(handler, "/link-archive-init", token, map[string]any{
		"totalChunks":   1,
		"totalBytes":    int64(len(slice)),
		"chunkSize":     linkArchiveChunkSize,
		"manifestHash":  b64(manifestHash),
		"archiveSha256": b64(archiveSha),
	})
	require.Equal(t, http.StatusOK, initRR.Code)
	var init struct{ ArchiveID, UploadToken string }
	require.NoError(t, json.Unmarshal(initRR.Body.Bytes(), &init))

	// Upload chunk via in-API endpoint (the postgres_bytea path).
	put := httptest.NewRequest(http.MethodPut,
		"/link-archive-chunk/"+init.ArchiveID+"/0", bytes.NewReader(slice))
	put.Header.Set("Content-Type", "application/octet-stream")
	put.Header.Set("Authorization", "Bearer "+token)
	put.Header.Set("X-Upload-Token", init.UploadToken)
	put.Header.Set("X-Chunk-Sha256", b64(chunkHash))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, put)
	require.Equal(t, http.StatusNoContent, rr.Code)

	// confirm-chunk for postgres_bytea is a no-op accept.
	body := fmt.Sprintf(`{"chunkSha256":%q,"chunkSize":%d}`, b64(chunkHash), len(slice))
	confirm := httptest.NewRequest(http.MethodPost,
		"/link-archive-confirm-chunk/"+init.ArchiveID+"/0", bytes.NewReader([]byte(body)))
	confirm.Header.Set("Content-Type", "application/json")
	confirm.Header.Set("Authorization", "Bearer "+token)
	confirm.Header.Set("X-Upload-Token", init.UploadToken)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, confirm)
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
}

func TestLinkArchive_ConfirmChunk_RejectsWhenStorageMissing(t *testing.T) {
	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	hash := sha256Sum([]byte("nothing"))
	initRR := postJSONWithAuth(handler, "/link-archive-init", token, map[string]any{
		"totalChunks":   1,
		"totalBytes":    int64(16),
		"chunkSize":     linkArchiveChunkSize,
		"manifestHash":  b64(make([]byte, 32)),
		"archiveSha256": b64(make([]byte, 32)),
	})
	require.Equal(t, http.StatusOK, initRR.Code)
	var init struct{ ArchiveID, UploadToken string }
	require.NoError(t, json.Unmarshal(initRR.Body.Bytes(), &init))

	body := fmt.Sprintf(`{"chunkSha256":%q,"chunkSize":16}`, b64(hash))
	confirm := httptest.NewRequest(http.MethodPost,
		"/link-archive-confirm-chunk/"+init.ArchiveID+"/0", bytes.NewReader([]byte(body)))
	confirm.Header.Set("Content-Type", "application/json")
	confirm.Header.Set("Authorization", "Bearer "+token)
	confirm.Header.Set("X-Upload-Token", init.UploadToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, confirm)
	require.Equal(t, http.StatusNotFound, rr.Code, rr.Body.String())
}

func TestLinkArchive_DownloadWindow_AfterFinalize(t *testing.T) {
	t.Setenv("LINK_ARCHIVE_USER_QUOTA", "10")
	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	slices := [][]byte{
		bytes.Repeat([]byte{0x11}, 8),
		bytes.Repeat([]byte{0x22}, 8),
	}
	archiveID, _, downloadToken, _, _ := uploadAndFinalize(t, store, token, slices)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	req := httptest.NewRequest(http.MethodPost,
		"/link-archive-download-window/"+archiveID,
		bytes.NewReader([]byte(`{"from":0,"to":2}`)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Download-Token", downloadToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var resp struct {
		From int `json:"from"`
		To   int `json:"to"`
		URLs []struct {
			Idx    int    `json:"idx"`
			URL    string `json:"url"`
			Method string `json:"method"`
		} `json:"urls"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	require.Len(t, resp.URLs, 2)
	for i, entry := range resp.URLs {
		require.Equal(t, i, entry.Idx)
		require.Equal(t, "GET", entry.Method)
		require.Contains(t, entry.URL, "/api/auth/link-archive-chunk/"+archiveID+"/"+itoa(i))
	}
}

func TestLinkArchive_Init_RejectsInvalidParams(t *testing.T) {
	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	cases := []struct {
		name string
		body map[string]any
	}{
		{"chunk size mismatch", map[string]any{
			"totalChunks": 1, "totalBytes": 16, "chunkSize": 1024,
			"manifestHash": b64(make([]byte, 32)), "archiveSha256": b64(make([]byte, 32)),
		}},
		{"manifest hash wrong length", map[string]any{
			"totalChunks": 1, "totalBytes": 1, "chunkSize": linkArchiveChunkSize,
			"manifestHash": b64(make([]byte, 16)), "archiveSha256": b64(make([]byte, 32)),
		}},
		{"non-positive total chunks", map[string]any{
			"totalChunks": 0, "totalBytes": 1, "chunkSize": linkArchiveChunkSize,
			"manifestHash": b64(make([]byte, 32)), "archiveSha256": b64(make([]byte, 32)),
		}},
		{"totalBytes inconsistent", map[string]any{
			// chunkBodyMax = chunkSize + 256; one byte beyond the per-chunk
			// ciphertext ceiling for a single-chunk archive must reject.
			"totalChunks": 1, "totalBytes": int64(linkArchiveChunkBodyMax) + 1, "chunkSize": linkArchiveChunkSize,
			"manifestHash": b64(make([]byte, 32)), "archiveSha256": b64(make([]byte, 32)),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := postJSONWithAuth(handler, "/link-archive-init", token, tc.body)
			require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
		})
	}
}

// Pins the per-chunk-body-max envelope: an honest client whose chunks
// are full plaintext (incompressible payloads such as already-encrypted
// transcript blobs) ships ciphertexts of size chunkSize + gzip framing
// + AES-GCM tag. The aggregate `totalBytes` constraint must permit
// totalChunks * (chunkSize + 256) since that is the per-chunk PUT
// ceiling already enforced when the chunk uploads. Regression guard
// for the 400 "totalBytes inconsistent" surfaced during real
// LinkDevice approval against active accounts.
func TestLinkArchive_Init_AcceptsBytesUpToChunkBodyMaxPerChunk(t *testing.T) {
	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	const chunks = 4
	body := map[string]any{
		"totalChunks":   chunks,
		"totalBytes":    int64(linkArchiveChunkBodyMax) * int64(chunks),
		"chunkSize":     linkArchiveChunkSize,
		"manifestHash":  b64(make([]byte, 32)),
		"archiveSha256": b64(make([]byte, 32)),
	}
	rr := postJSONWithAuth(handler, "/link-archive-init", token, body)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	// One byte beyond the per-chunk ceiling for the same archive must reject.
	body["totalBytes"] = int64(linkArchiveChunkBodyMax)*int64(chunks) + 1
	rr2 := postJSONWithAuth(handler, "/link-archive-init", token, body)
	require.Equal(t, http.StatusBadRequest, rr2.Code, rr2.Body.String())
}

func TestLinkArchive_Init_EnforcesPerUserQuota(t *testing.T) {
	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	body := map[string]any{
		"totalChunks":   1,
		"totalBytes":    1,
		"chunkSize":     linkArchiveChunkSize,
		"manifestHash":  b64(make([]byte, 32)),
		"archiveSha256": b64(make([]byte, 32)),
	}

	// First archive within quota.
	rr := postJSONWithAuth(handler, "/link-archive-init", token, body)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	// Second archive exceeds default quota of 1.
	rr = postJSONWithAuth(handler, "/link-archive-init", token, body)
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())
}

func TestLinkArchive_Init_AutoSupersedesAbandonedPriorArchive(t *testing.T) {
	// Grace = 60s. Default sliding TTL = 60min. The "abandoned" prior
	// archive has expires_at far in the past (simulating a NEW-device tab
	// killed before the import-failure DELETE could fire). The next
	// init for the same user must transparently tear it down and
	// allocate the new archive.
	t.Setenv("LINK_ARCHIVE_SUPERSEDE_GRACE_SECONDS", "60")

	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	body := map[string]any{
		"totalChunks":   1,
		"totalBytes":    1,
		"chunkSize":     linkArchiveChunkSize,
		"manifestHash":  b64(make([]byte, 32)),
		"archiveSha256": b64(make([]byte, 32)),
	}

	first := postJSONWithAuth(handler, "/link-archive-init", token, body)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	var firstResp struct{ ArchiveID string }
	require.NoError(t, json.Unmarshal(first.Body.Bytes(), &firstResp))

	// Pretend the NEW-device tab died: reach into the fake store and
	// pull the prior archive's expires_at far enough back that
	// (now + sliding_ttl - grace) > expires_at.
	prior, ok := store.archives[firstResp.ArchiveID]
	require.True(t, ok)
	prior.ExpiresAt = time.Now().UTC().Add(-time.Hour)

	second := postJSONWithAuth(handler, "/link-archive-init", token, body)
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	var secondResp struct{ ArchiveID string }
	require.NoError(t, json.Unmarshal(second.Body.Bytes(), &secondResp))
	require.NotEqual(t, firstResp.ArchiveID, secondResp.ArchiveID)

	// Prior abandoned archive must be gone.
	_, stillThere := store.archives[firstResp.ArchiveID]
	require.False(t, stillThere, "abandoned prior archive should have been superseded")

	// Quota slot is now occupied by the second archive only.
	_, ok = store.archives[secondResp.ArchiveID]
	require.True(t, ok)
}

func TestLinkArchive_Init_DoesNotSupersedeRecentlyTouchedPriorArchive(t *testing.T) {
	// A genuinely-active concurrent session whose expires_at is fresh
	// (because some other request just refreshed it) must NOT be
	// superseded — quota check fires and the second init returns 409.
	t.Setenv("LINK_ARCHIVE_SUPERSEDE_GRACE_SECONDS", "60")

	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	body := map[string]any{
		"totalChunks":   1,
		"totalBytes":    1,
		"chunkSize":     linkArchiveChunkSize,
		"manifestHash":  b64(make([]byte, 32)),
		"archiveSha256": b64(make([]byte, 32)),
	}

	first := postJSONWithAuth(handler, "/link-archive-init", token, body)
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())

	second := postJSONWithAuth(handler, "/link-archive-init", token, body)
	require.Equal(t, http.StatusConflict, second.Code, second.Body.String())
}

func TestLinkArchive_Init_EnforcesStagingBytesCap(t *testing.T) {
	t.Setenv("LINK_ARCHIVE_STAGING_BYTES_CAP", "32")
	t.Setenv("LINK_ARCHIVE_USER_QUOTA", "10") // quota out of the way for this test.

	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	rr := postJSONWithAuth(handler, "/link-archive-init", token, map[string]any{
		"totalChunks":   1,
		"totalBytes":    33,
		"chunkSize":     linkArchiveChunkSize,
		"manifestHash":  b64(make([]byte, 32)),
		"archiveSha256": b64(make([]byte, 32)),
	})
	require.Equal(t, http.StatusServiceUnavailable, rr.Code, rr.Body.String())
	require.NotEmpty(t, rr.Header().Get("Retry-After"))
}

func TestLinkArchive_Upload_HashIdempotency(t *testing.T) {
	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	slice := bytes.Repeat([]byte{0xab}, 32)
	chunkHash := sha256Sum(slice)
	manifestHash := sha256Sum(chunkHash)
	archiveSha := sha256Sum(slice)

	initRR := postJSONWithAuth(handler, "/link-archive-init", token, map[string]any{
		"totalChunks":   1,
		"totalBytes":    int64(len(slice)),
		"chunkSize":     linkArchiveChunkSize,
		"manifestHash":  b64(manifestHash),
		"archiveSha256": b64(archiveSha),
	})
	require.Equal(t, http.StatusOK, initRR.Code, initRR.Body.String())
	var init struct{ ArchiveID, UploadToken string }
	require.NoError(t, json.Unmarshal(initRR.Body.Bytes(), &init))

	doUpload := func(idx int, body []byte, hash []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut,
			"/link-archive-chunk/"+init.ArchiveID+"/"+itoa(idx),
			bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Upload-Token", init.UploadToken)
		req.Header.Set("X-Chunk-Sha256", b64(hash))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr
	}

	rr := doUpload(0, slice, chunkHash)
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

	// Same hash retransmit -> idempotent 204.
	rr = doUpload(0, slice, chunkHash)
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

	// Different hash for same idx -> 409 + stored row unchanged.
	other := bytes.Repeat([]byte{0xcd}, 32)
	otherHash := sha256Sum(other)
	rr = doUpload(0, other, otherHash)
	require.Equal(t, http.StatusConflict, rr.Code, rr.Body.String())

	stored := store.chunks[init.ArchiveID][0]
	require.True(t, bytes.Equal(stored.hash, chunkHash))
}

func TestLinkArchive_FullRoundTrip(t *testing.T) {
	t.Setenv("LINK_ARCHIVE_USER_QUOTA", "10")
	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	slices := [][]byte{
		bytes.Repeat([]byte{0x10}, 64),
		bytes.Repeat([]byte{0x20}, 32),
	}
	archiveID, _, downloadToken, manifestHash, archiveSha := uploadAndFinalize(t, store, token, slices)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	manifestReq := httptest.NewRequest(http.MethodGet,
		"/link-archive-manifest/"+archiveID, nil)
	manifestReq.Header.Set("X-Download-Token", downloadToken)
	manifestRR := httptest.NewRecorder()
	handler.ServeHTTP(manifestRR, manifestReq)
	require.Equal(t, http.StatusOK, manifestRR.Code, manifestRR.Body.String())

	var manifest struct {
		TotalChunks   int      `json:"totalChunks"`
		ChunkHashes   []string `json:"chunkHashes"`
		ManifestHash  string   `json:"manifestHash"`
		ArchiveSha256 string   `json:"archiveSha256"`
	}
	require.NoError(t, json.Unmarshal(manifestRR.Body.Bytes(), &manifest))
	assert.Equal(t, 2, manifest.TotalChunks)
	assert.Equal(t, b64(manifestHash), manifest.ManifestHash)
	assert.Equal(t, b64(archiveSha), manifest.ArchiveSha256)

	for i, expectedSlice := range slices {
		downloadReq := httptest.NewRequest(http.MethodGet,
			"/link-archive-chunk/"+archiveID+"/"+itoa(i), nil)
		downloadReq.Header.Set("X-Download-Token", downloadToken)
		downloadRR := httptest.NewRecorder()
		handler.ServeHTTP(downloadRR, downloadReq)
		require.Equal(t, http.StatusOK, downloadRR.Code, downloadRR.Body.String())
		body, _ := io.ReadAll(downloadRR.Body)
		assert.Equal(t, expectedSlice, body)
	}
}

func TestLinkArchive_Finalize_RejectsMissingChunks(t *testing.T) {
	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	chunkA := bytes.Repeat([]byte{0xaa}, 16)
	chunkB := bytes.Repeat([]byte{0xbb}, 16)
	hashA, hashB := sha256Sum(chunkA), sha256Sum(chunkB)
	manifestHash := sha256Sum(append(append([]byte{}, hashA...), hashB...))
	archiveSha := sha256Sum(append(append([]byte{}, chunkA...), chunkB...))

	initRR := postJSONWithAuth(handler, "/link-archive-init", token, map[string]any{
		"totalChunks":   2,
		"totalBytes":    int64(len(chunkA) + len(chunkB)),
		"chunkSize":     linkArchiveChunkSize,
		"manifestHash":  b64(manifestHash),
		"archiveSha256": b64(archiveSha),
	})
	require.Equal(t, http.StatusOK, initRR.Code)
	var init struct{ ArchiveID, UploadToken string }
	require.NoError(t, json.Unmarshal(initRR.Body.Bytes(), &init))

	put := httptest.NewRequest(http.MethodPut,
		"/link-archive-chunk/"+init.ArchiveID+"/0", bytes.NewReader(chunkA))
	put.Header.Set("Content-Type", "application/octet-stream")
	put.Header.Set("Authorization", "Bearer "+token)
	put.Header.Set("X-Upload-Token", init.UploadToken)
	put.Header.Set("X-Chunk-Sha256", b64(hashA))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, put)
	require.Equal(t, http.StatusNoContent, rr.Code)

	finReq := httptest.NewRequest(http.MethodPost,
		"/link-archive-finalize/"+init.ArchiveID, nil)
	finReq.Header.Set("Authorization", "Bearer "+token)
	finReq.Header.Set("X-Upload-Token", init.UploadToken)
	finRR := httptest.NewRecorder()
	handler.ServeHTTP(finRR, finReq)
	require.Equal(t, http.StatusConflict, finRR.Code, finRR.Body.String())

	var body struct {
		Error   string `json:"error"`
		Missing []int  `json:"missing"`
	}
	require.NoError(t, json.Unmarshal(finRR.Body.Bytes(), &body))
	require.Equal(t, []int{1}, body.Missing)
}

func TestLinkArchive_DownloadBeforeFinalize_TooEarly(t *testing.T) {
	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	manifestHash := sha256Sum(make([]byte, 32))
	archiveSha := sha256Sum(make([]byte, 32))
	initRR := postJSONWithAuth(handler, "/link-archive-init", token, map[string]any{
		"totalChunks":   1,
		"totalBytes":    int64(16),
		"chunkSize":     linkArchiveChunkSize,
		"manifestHash":  b64(manifestHash),
		"archiveSha256": b64(archiveSha),
	})
	require.Equal(t, http.StatusOK, initRR.Code)
	var init struct{ ArchiveID, DownloadToken string }
	require.NoError(t, json.Unmarshal(initRR.Body.Bytes(), &init))

	manifestReq := httptest.NewRequest(http.MethodGet,
		"/link-archive-manifest/"+init.ArchiveID, nil)
	manifestReq.Header.Set("X-Download-Token", init.DownloadToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, manifestReq)
	require.Equal(t, http.StatusTooEarly, rr.Code, rr.Body.String())
}

func TestLinkArchive_Ack_TransitionsToAcknowledged(t *testing.T) {
	t.Setenv("LINK_ARCHIVE_USER_QUOTA", "10")
	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	slices := [][]byte{bytes.Repeat([]byte{0x33}, 8)}
	archiveID, _, downloadToken, _, _ := uploadAndFinalize(t, store, token, slices)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	require.Equal(t, db.LinkArchiveStateAvailable, store.archives[archiveID].State)

	ackReq := httptest.NewRequest(http.MethodPost, "/link-archive-ack/"+archiveID, nil)
	ackReq.Header.Set("X-Download-Token", downloadToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, ackReq)
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	require.Equal(t, db.LinkArchiveStateAcknowledged, store.archives[archiveID].State)
}

func TestLinkArchive_DeleteByDownloadToken_RemovesBytesAndRow(t *testing.T) {
	t.Setenv("LINK_ARCHIVE_USER_QUOTA", "10")
	store := newFakeLinkArchiveStore()
	userID := uuid.NewString()
	token := makeAuth(store.mu, userID)
	slices := [][]byte{bytes.Repeat([]byte{0x11}, 8)}
	archiveID, _, downloadToken, _, _ := uploadAndFinalize(t, store, token, slices)
	handler := AuthRoutes(store.mu, testJWTSecret, testJWTExpiry, nil)

	// Sanity: blob is present.
	require.NotEmpty(t, store.blobs)

	delReq := httptest.NewRequest(http.MethodDelete,
		"/link-archive/"+archiveID, nil)
	delReq.Header.Set("X-Download-Token", downloadToken)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, delReq)
	require.Equal(t, http.StatusNoContent, rr.Code)

	_, exists := store.archives[archiveID]
	require.False(t, exists)
	require.Empty(t, store.blobs, "delete must clean storage backend objects too")
}

func TestLinkArchive_Tiered_GC_Listing(t *testing.T) {
	store := newFakeLinkArchiveStore()
	now := time.Now().UTC()
	add := func(state string, expiresOffset, hardOffset time.Duration) string {
		id := uuid.NewString()
		store.archives[id] = &db.LinkArchive{
			ID:             id,
			UserID:         "u",
			State:          state,
			ExpiresAt:      now.Add(expiresOffset),
			HardDeadlineAt: now.Add(hardOffset),
		}
		return id
	}
	ack := add(db.LinkArchiveStateAcknowledged, time.Hour, 30*time.Hour)
	abort := add(db.LinkArchiveStateAborted, time.Hour, 30*time.Hour)
	failedFresh := add(db.LinkArchiveStateTerminalFailure, -23*time.Hour, 30*time.Hour) // within 24h grace
	failedStale := add(db.LinkArchiveStateTerminalFailure, -25*time.Hour, 30*time.Hour) // past 24h grace
	idleFresh := add(db.LinkArchiveStateImporting, time.Hour, 30*time.Hour)             // sliding window OK
	idleStale := add(db.LinkArchiveStateImporting, -10*time.Minute, 30*time.Hour)       // expired
	hardOver := add(db.LinkArchiveStateImporting, time.Hour, -1*time.Minute)            // hard deadline reached

	ids, err := store.mu.ListGcEligibleLinkArchives(context.Background(), 100)
	require.NoError(t, err)

	got := make(map[string]bool, len(ids))
	for _, id := range ids {
		got[id] = true
	}
	require.True(t, got[ack], "acknowledged is GC-eligible immediately")
	require.True(t, got[abort], "aborted is GC-eligible immediately")
	require.False(t, got[failedFresh], "terminal_failure within 24h grace is not eligible")
	require.True(t, got[failedStale], "terminal_failure past 24h grace is eligible")
	require.False(t, got[idleFresh], "active state with sliding window OK is not eligible")
	require.True(t, got[idleStale], "active state past sliding window is eligible")
	require.True(t, got[hardOver], "hard deadline reached is always eligible")
}
