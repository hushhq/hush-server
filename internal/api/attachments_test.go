package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/hushhq/hush-server/internal/models"
	"github.com/hushhq/hush-server/internal/storage"
)

// fakeBackend implements storage.Backend just well enough for the
// presign + delete tests. Any unused method panics so a regression
// that calls the wrong path is loud.
type fakeBackend struct {
	presignPutFn     func(ctx context.Context, key string, ttl time.Duration) (storage.PresignedURL, error)
	presignGetFn     func(ctx context.Context, key string, ttl time.Duration) (storage.PresignedURL, error)
	deleteFn         func(ctx context.Context, key string) error
	deleteCalledKey  string
	deleteCalledKeys []string
}

func (b *fakeBackend) Kind() storage.BackendKind { return storage.BackendS3 }
func (b *fakeBackend) Put(context.Context, string, io.Reader, int64) (storage.PutResult, error) {
	return storage.PutResult{}, errors.New("not used")
}
func (b *fakeBackend) Get(context.Context, string) (io.ReadCloser, int64, error) {
	return nil, 0, errors.New("not used")
}
func (b *fakeBackend) Delete(ctx context.Context, key string) error {
	b.deleteCalledKey = key
	b.deleteCalledKeys = append(b.deleteCalledKeys, key)
	if b.deleteFn != nil {
		return b.deleteFn(ctx, key)
	}
	return nil
}
func (b *fakeBackend) Exists(context.Context, string) (bool, error) { return true, nil }
func (b *fakeBackend) PresignPut(ctx context.Context, key string, ttl time.Duration) (storage.PresignedURL, error) {
	if b.presignPutFn != nil {
		return b.presignPutFn(ctx, key, ttl)
	}
	return storage.PresignedURL{
		URL:       "https://example.test/upload/" + key,
		Method:    http.MethodPut,
		ExpiresAt: time.Now().Add(ttl),
	}, nil
}
func (b *fakeBackend) PresignGet(ctx context.Context, key string, ttl time.Duration) (storage.PresignedURL, error) {
	if b.presignGetFn != nil {
		return b.presignGetFn(ctx, key, ttl)
	}
	return storage.PresignedURL{
		URL:       "https://example.test/download/" + key,
		Method:    http.MethodGet,
		ExpiresAt: time.Now().Add(ttl),
	}, nil
}

// attachmentRouterForChannel builds a router shaped like the production
// nesting: /{channelId}/attachments/* mounted on a chi root, with the
// userID injected upstream the way RequireAuth would. The {channelId}
// URL param resolves through chi as it would in the real ChannelRoutes.
func attachmentRouterForChannel(store *mockStore, backend storage.Backend, userID string) http.Handler {
	factory := AttachmentBackendFactory(func() (storage.Backend, error) {
		if backend == nil {
			return nil, errors.New("backend disabled")
		}
		return backend, nil
	})
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(withUserID(req.Context(), userID)))
		})
	})
	r.Route("/{channelId}/attachments", func(r chi.Router) {
		r.Mount("/", ChannelAttachmentRoutes(store, factory))
	})
	return r
}

func TestAttachmentPresign_HappyPath(t *testing.T) {
	channelID := uuid.NewString()
	userID := uuid.NewString()
	storedRow := &models.Attachment{
		ID:          uuid.NewString(),
		ChannelID:   channelID,
		OwnerID:     userID,
		StorageKey:  "attachments/" + channelID + "/some-key",
		Size:        12345,
		ContentType: "image/png",
		CreatedAt:   time.Now(),
	}
	store := &mockStore{}
	store.isChannelMemberFn = func(_ context.Context, gotChannel, gotUser string) (bool, error) {
		assert.Equal(t, channelID, gotChannel)
		assert.Equal(t, userID, gotUser)
		return true, nil
	}
	store.insertAttachmentFn = func(_ context.Context, ch, owner, key, ct string, size int64) (*models.Attachment, error) {
		assert.Equal(t, channelID, ch)
		assert.Equal(t, userID, owner)
		assert.True(t, strings.HasPrefix(key, "attachments/"+channelID+"/"))
		assert.Equal(t, "image/png", ct)
		assert.Equal(t, int64(12345), size)
		storedRow.StorageKey = key
		return storedRow, nil
	}
	backend := &fakeBackend{}
	router := attachmentRouterForChannel(store, backend, userID)

	body := strings.NewReader(`{"size":12345,"contentType":"image/png"}`)
	req := httptest.NewRequest(http.MethodPost, "/"+channelID+"/attachments/presign", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp presignResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, storedRow.ID, resp.ID)
	assert.Contains(t, resp.UploadURL, storedRow.StorageKey)
	assert.Equal(t, http.MethodPut, resp.Method)
}

func TestAttachmentPresign_RejectsOversize(t *testing.T) {
	store := &mockStore{}
	store.isChannelMemberFn = func(context.Context, string, string) (bool, error) { return true, nil }
	channelID := uuid.NewString()
	userID := uuid.NewString()
	router := attachmentRouterForChannel(store, &fakeBackend{}, userID)

	body := strings.NewReader(fmt.Sprintf(`{"size":%d,"contentType":"image/png"}`, MaxAttachmentBytes+1))
	req := httptest.NewRequest(http.MethodPost, "/"+channelID+"/attachments/presign", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
}

func TestAttachmentPresign_UsesInstanceMaxAttachmentBytes(t *testing.T) {
	store := &mockStore{}
	store.getInstanceConfigFn = func(context.Context) (*models.InstanceConfig, error) {
		return &models.InstanceConfig{
			MaxAttachmentBytes:   2048,
			MessageRetentionDays: 90,
		}, nil
	}
	store.isChannelMemberFn = func(context.Context, string, string) (bool, error) { return true, nil }
	channelID := uuid.NewString()
	userID := uuid.NewString()
	router := attachmentRouterForChannel(store, &fakeBackend{}, userID)

	body := strings.NewReader(`{"size":4096,"contentType":"image/png"}`)
	req := httptest.NewRequest(http.MethodPost, "/"+channelID+"/attachments/presign", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusRequestEntityTooLarge, rr.Code)
}

func TestAttachmentPresign_EnforcesGuildQuotaByTombstoningOldest(t *testing.T) {
	channelID := uuid.NewString()
	userID := uuid.NewString()
	oldID := uuid.NewString()
	oldKey := "attachments/" + channelID + "/old"
	newID := uuid.NewString()
	newKey := "attachments/" + channelID + "/new"
	store := &mockStore{}
	quota := int64(100)
	store.getInstanceConfigFn = func(context.Context) (*models.InstanceConfig, error) {
		return &models.InstanceConfig{
			MaxAttachmentBytes:             MaxAttachmentBytes,
			MaxGuildAttachmentStorageBytes: &quota,
			MessageRetentionDays:           90,
		}, nil
	}
	store.isChannelMemberFn = func(context.Context, string, string) (bool, error) { return true, nil }
	store.insertAttachmentFn = func(context.Context, string, string, string, string, int64) (*models.Attachment, error) {
		return &models.Attachment{
			ID:          newID,
			ChannelID:   channelID,
			OwnerID:     userID,
			StorageKey:  newKey,
			Size:        50,
			ContentType: "image/png",
			CreatedAt:   time.Now(),
		}, nil
	}
	store.listAttachmentsForGuildQuotaFn = func(context.Context, string) (string, []models.Attachment, error) {
		return "server-1", []models.Attachment{
			{
				ID:          oldID,
				ChannelID:   channelID,
				OwnerID:     userID,
				StorageKey:  oldKey,
				Size:        75,
				ContentType: "image/png",
				CreatedAt:   time.Now().Add(-time.Hour),
			},
			{
				ID:          newID,
				ChannelID:   channelID,
				OwnerID:     userID,
				StorageKey:  newKey,
				Size:        50,
				ContentType: "image/png",
				CreatedAt:   time.Now(),
			},
		}, nil
	}
	var tombstoned []string
	store.softDeleteAttachmentsByIDFn = func(_ context.Context, ids []string) ([]models.Attachment, error) {
		tombstoned = append([]string(nil), ids...)
		return []models.Attachment{{
			ID:         oldID,
			ChannelID:  channelID,
			OwnerID:    userID,
			StorageKey: oldKey,
			Size:       75,
			CreatedAt:  time.Now().Add(-time.Hour),
		}}, nil
	}
	backend := &fakeBackend{}
	router := attachmentRouterForChannel(store, backend, userID)

	body := strings.NewReader(`{"size":50,"contentType":"image/png"}`)
	req := httptest.NewRequest(http.MethodPost, "/"+channelID+"/attachments/presign", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.Equal(t, []string{oldID}, tombstoned)
	assert.Equal(t, []string{oldKey}, backend.deleteCalledKeys)
}

func TestAttachmentPresign_RejectsDisallowedMime(t *testing.T) {
	store := &mockStore{}
	store.isChannelMemberFn = func(context.Context, string, string) (bool, error) { return true, nil }
	channelID := uuid.NewString()
	userID := uuid.NewString()
	router := attachmentRouterForChannel(store, &fakeBackend{}, userID)

	body := strings.NewReader(`{"size":1024,"contentType":"application/x-evil"}`)
	req := httptest.NewRequest(http.MethodPost, "/"+channelID+"/attachments/presign", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnsupportedMediaType, rr.Code)
}

func TestAttachmentPresign_RejectsNonChannelMember(t *testing.T) {
	store := &mockStore{}
	store.isChannelMemberFn = func(context.Context, string, string) (bool, error) { return false, nil }
	channelID := uuid.NewString()
	userID := uuid.NewString()
	router := attachmentRouterForChannel(store, &fakeBackend{}, userID)

	body := strings.NewReader(`{"size":1024,"contentType":"image/png"}`)
	req := httptest.NewRequest(http.MethodPost, "/"+channelID+"/attachments/presign", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestAttachmentDelete_OwnerOnly_SoftDeletesAndCallsBackend(t *testing.T) {
	channelID := uuid.NewString()
	userID := uuid.NewString()
	row := &models.Attachment{
		ID:          uuid.NewString(),
		ChannelID:   channelID,
		OwnerID:     userID,
		StorageKey:  "attachments/" + channelID + "/abc",
		ContentType: "image/png",
		Size:        100,
	}
	store := &mockStore{}
	store.softDeleteAttachmentFn = func(_ context.Context, id, owner string) (*models.Attachment, error) {
		assert.Equal(t, row.ID, id)
		assert.Equal(t, userID, owner)
		return row, nil
	}
	backend := &fakeBackend{}
	factory := AttachmentBackendFactory(func() (storage.Backend, error) { return backend, nil })

	h := &attachmentHandler{store: store, backendFactory: factory}
	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(withUserID(r.Context(), userID)))
		})
	})
	mux.Delete("/{id}", h.delete)

	req := httptest.NewRequest(http.MethodDelete, "/"+row.ID, nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
	assert.Equal(t, row.StorageKey, backend.deleteCalledKey)
}

func TestAttachmentDelete_NotOwner_Returns404(t *testing.T) {
	userID := uuid.NewString()
	store := &mockStore{}
	store.softDeleteAttachmentFn = func(context.Context, string, string) (*models.Attachment, error) {
		return nil, pgx.ErrNoRows
	}
	backend := &fakeBackend{}
	factory := AttachmentBackendFactory(func() (storage.Backend, error) { return backend, nil })
	h := &attachmentHandler{store: store, backendFactory: factory}

	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(withUserID(r.Context(), userID)))
		})
	})
	mux.Delete("/{id}", h.delete)

	req := httptest.NewRequest(http.MethodDelete, "/"+uuid.NewString(), nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
	assert.Equal(t, "", backend.deleteCalledKey)
}

func TestAttachmentDownload_NotChannelMember_Returns403(t *testing.T) {
	row := &models.Attachment{
		ID:          uuid.NewString(),
		ChannelID:   uuid.NewString(),
		OwnerID:     uuid.NewString(),
		StorageKey:  "attachments/x/y",
		ContentType: "image/png",
		Size:        1,
	}
	store := &mockStore{}
	store.getAttachmentByIDFn = func(context.Context, string) (*models.Attachment, error) {
		return row, nil
	}
	store.isChannelMemberFn = func(context.Context, string, string) (bool, error) { return false, nil }
	backend := &fakeBackend{}
	factory := AttachmentBackendFactory(func() (storage.Backend, error) { return backend, nil })
	h := &attachmentHandler{store: store, backendFactory: factory}

	otherUser := uuid.NewString()
	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(withUserID(r.Context(), otherUser)))
		})
	})
	mux.Get("/{id}/download", h.download)

	req := httptest.NewRequest(http.MethodGet, "/"+row.ID+"/download", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestAttachmentDownload_DeletedAttachmentReturnsGone(t *testing.T) {
	deletedAt := time.Now()
	row := &models.Attachment{
		ID:          uuid.NewString(),
		ChannelID:   uuid.NewString(),
		OwnerID:     uuid.NewString(),
		StorageKey:  "attachments/x/y",
		ContentType: "image/png",
		Size:        1,
		DeletedAt:   &deletedAt,
	}
	store := &mockStore{}
	store.getAttachmentByIDFn = func(context.Context, string) (*models.Attachment, error) {
		return row, nil
	}
	backend := &fakeBackend{}
	factory := AttachmentBackendFactory(func() (storage.Backend, error) { return backend, nil })
	h := &attachmentHandler{store: store, backendFactory: factory}

	userID := uuid.NewString()
	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(withUserID(r.Context(), userID)))
		})
	})
	mux.Get("/{id}/download", h.download)

	req := httptest.NewRequest(http.MethodGet, "/"+row.ID+"/download", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusGone, rr.Code)
}

// postgresByteaBackend is a fake whose Kind reports postgres_bytea so the
// attachment handler routes through the in-API blob fallback instead of the
// presigned URL path. Captures the bytes the in-API PUT writes so a test can
// assert the upload was forwarded to storage.
type postgresByteaBackend struct {
	stored map[string][]byte
}

func (b *postgresByteaBackend) Kind() storage.BackendKind {
	return storage.BackendPostgresBytea
}
func (b *postgresByteaBackend) Put(_ context.Context, key string, r io.Reader, size int64) (storage.PutResult, error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return storage.PutResult{}, err
	}
	if b.stored == nil {
		b.stored = map[string][]byte{}
	}
	b.stored[key] = buf
	return storage.PutResult{Size: int64(len(buf))}, nil
}
func (b *postgresByteaBackend) Get(_ context.Context, key string) (io.ReadCloser, int64, error) {
	v, ok := b.stored[key]
	if !ok {
		return nil, 0, storage.ErrNotFound
	}
	return io.NopCloser(strings.NewReader(string(v))), int64(len(v)), nil
}
func (b *postgresByteaBackend) Delete(_ context.Context, key string) error {
	delete(b.stored, key)
	return nil
}
func (b *postgresByteaBackend) Exists(_ context.Context, key string) (bool, error) {
	_, ok := b.stored[key]
	return ok, nil
}
func (b *postgresByteaBackend) PresignPut(_ context.Context, key string, _ time.Duration) (storage.PresignedURL, error) {
	// Mirror the bug the attachment handler now works around: the legacy
	// implementation routed attachments through the device-link archive
	// chunk endpoint. The test relies on the handler NOT delegating here
	// for postgres_bytea — if it does the URL below makes the regression
	// loud.
	return storage.PresignedURL{
		URL:    "/api/auth/link-archive-chunk/" + key,
		Method: http.MethodPut,
	}, nil
}
func (b *postgresByteaBackend) PresignGet(_ context.Context, key string, _ time.Duration) (storage.PresignedURL, error) {
	return storage.PresignedURL{
		URL:    "/api/auth/link-archive-chunk/" + key,
		Method: http.MethodGet,
	}, nil
}

// TestAttachmentPresign_PostgresBytea_RoutesThroughInAPIBlobFallback pins
// the bug fix for the desktop attachment-upload regression: when the resolved
// backend cannot presign natively, the handler MUST return an attachment-
// specific in-API URL, NOT the device-link archive chunk path.
func TestAttachmentPresign_PostgresBytea_RoutesThroughInAPIBlobFallback(t *testing.T) {
	channelID := uuid.NewString()
	userID := uuid.NewString()
	row := &models.Attachment{
		ID:          uuid.NewString(),
		ChannelID:   channelID,
		OwnerID:     userID,
		StorageKey:  "attachments/" + channelID + "/blob-key",
		Size:        2048,
		ContentType: "image/png",
		CreatedAt:   time.Now(),
	}
	store := &mockStore{}
	store.isChannelMemberFn = func(context.Context, string, string) (bool, error) { return true, nil }
	store.insertAttachmentFn = func(_ context.Context, _, _, key, _ string, _ int64) (*models.Attachment, error) {
		row.StorageKey = key
		return row, nil
	}

	backend := &postgresByteaBackend{}
	router := attachmentRouterForChannel(store, backend, userID)

	body := strings.NewReader(`{"size":2048,"contentType":"image/png"}`)
	req := httptest.NewRequest(http.MethodPost, "/"+channelID+"/attachments/presign", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp presignResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, row.ID, resp.ID)
	assert.Equal(t, "/api/attachments/"+row.ID+"/blob", resp.UploadURL)
	assert.Equal(t, http.MethodPut, resp.Method)
	assert.NotContains(t, resp.UploadURL, "link-archive-chunk")
}

// TestAttachmentDownload_PostgresBytea_RoutesThroughInAPIBlobFallback covers
// the symmetric GET path: the legacy backend hand-off returned the device-link
// chunk URL on download too; we now return the attachment-blob route.
func TestAttachmentDownload_PostgresBytea_RoutesThroughInAPIBlobFallback(t *testing.T) {
	row := &models.Attachment{
		ID:          uuid.NewString(),
		ChannelID:   uuid.NewString(),
		OwnerID:     uuid.NewString(),
		StorageKey:  "attachments/x/y",
		ContentType: "image/png",
		Size:        1024,
	}
	store := &mockStore{}
	store.getAttachmentByIDFn = func(context.Context, string) (*models.Attachment, error) {
		return row, nil
	}
	store.isChannelMemberFn = func(context.Context, string, string) (bool, error) { return true, nil }

	backend := &postgresByteaBackend{}
	factory := AttachmentBackendFactory(func() (storage.Backend, error) { return backend, nil })
	h := &attachmentHandler{store: store, backendFactory: factory}

	userID := uuid.NewString()
	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(withUserID(r.Context(), userID)))
		})
	})
	mux.Get("/{id}/download", h.download)

	req := httptest.NewRequest(http.MethodGet, "/"+row.ID+"/download", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp downloadResponse
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, "/api/attachments/"+row.ID+"/blob", resp.URL)
}

// TestAttachmentPutBlob_HappyPath_WritesCiphertextThroughInAPIRoute exercises
// the new PUT /api/attachments/{id}/blob handler end-to-end via the in-process
// backend. Verifies the uploader auth gate, channel-membership gate, and that
// bytes reach the backend's Put under the row's storage key.
func TestAttachmentPutBlob_HappyPath_WritesCiphertextThroughInAPIRoute(t *testing.T) {
	row := &models.Attachment{
		ID:          uuid.NewString(),
		ChannelID:   uuid.NewString(),
		OwnerID:     uuid.NewString(),
		StorageKey:  "attachments/x/y",
		ContentType: "image/png",
		Size:        4,
	}
	store := &mockStore{}
	store.getAttachmentByIDFn = func(context.Context, string) (*models.Attachment, error) { return row, nil }
	store.isChannelMemberFn = func(context.Context, string, string) (bool, error) { return true, nil }

	backend := &postgresByteaBackend{}
	factory := AttachmentBackendFactory(func() (storage.Backend, error) { return backend, nil })
	h := &attachmentHandler{store: store, backendFactory: factory}

	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(withUserID(r.Context(), row.OwnerID)))
		})
	})
	mux.Put("/{id}/blob", h.putBlob)

	req := httptest.NewRequest(http.MethodPut, "/"+row.ID+"/blob", strings.NewReader("ABCD"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	assert.Equal(t, []byte("ABCD"), backend.stored[row.StorageKey])
}

// TestAttachmentPutBlob_NotUploader_Returns403 prevents another channel member
// from completing an upload someone else presigned. The presign row holds the
// uploader id; only that id may write the body.
func TestAttachmentPutBlob_NotUploader_Returns403(t *testing.T) {
	row := &models.Attachment{
		ID:         uuid.NewString(),
		ChannelID:  uuid.NewString(),
		OwnerID:    uuid.NewString(),
		StorageKey: "attachments/x/y",
		Size:       1,
	}
	store := &mockStore{}
	store.getAttachmentByIDFn = func(context.Context, string) (*models.Attachment, error) { return row, nil }
	backend := &postgresByteaBackend{}
	factory := AttachmentBackendFactory(func() (storage.Backend, error) { return backend, nil })
	h := &attachmentHandler{store: store, backendFactory: factory}

	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(withUserID(r.Context(), uuid.NewString())))
		})
	})
	mux.Put("/{id}/blob", h.putBlob)

	req := httptest.NewRequest(http.MethodPut, "/"+row.ID+"/blob", strings.NewReader("X"))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}

// TestAttachmentGetBlob_StreamsCiphertextToChannelMember asserts the in-API
// GET fallback returns the stored bytes and tags the response as opaque.
func TestAttachmentGetBlob_StreamsCiphertextToChannelMember(t *testing.T) {
	row := &models.Attachment{
		ID:         uuid.NewString(),
		ChannelID:  uuid.NewString(),
		OwnerID:    uuid.NewString(),
		StorageKey: "attachments/x/y",
		Size:       3,
	}
	store := &mockStore{}
	store.getAttachmentByIDFn = func(context.Context, string) (*models.Attachment, error) { return row, nil }
	store.isChannelMemberFn = func(context.Context, string, string) (bool, error) { return true, nil }
	backend := &postgresByteaBackend{stored: map[string][]byte{row.StorageKey: []byte("xyz")}}
	factory := AttachmentBackendFactory(func() (storage.Backend, error) { return backend, nil })
	h := &attachmentHandler{store: store, backendFactory: factory}

	mux := chi.NewRouter()
	mux.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(withUserID(r.Context(), uuid.NewString())))
		})
	})
	mux.Get("/{id}/blob", h.getBlob)

	req := httptest.NewRequest(http.MethodGet, "/"+row.ID+"/blob", nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "xyz", rr.Body.String())
	assert.Equal(t, "application/octet-stream", rr.Header().Get("Content-Type"))
}

// silence unused-helper warnings for the postgres_bytea backend pieces this
// file uses only some of (Put/Get/Exists/PresignGet exist for interface
// satisfaction). pgx import is retained because other tests in the file
// reference it.
var _ = pgx.ErrNoRows
var _ = fmt.Sprintf
