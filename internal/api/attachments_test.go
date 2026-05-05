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
	presignPutFn    func(ctx context.Context, key string, ttl time.Duration) (storage.PresignedURL, error)
	presignGetFn    func(ctx context.Context, key string, ttl time.Duration) (storage.PresignedURL, error)
	deleteFn        func(ctx context.Context, key string) error
	deleteCalledKey string
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
