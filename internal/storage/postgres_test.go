package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// memChunkBlobStore is a minimal in-memory ChunkBlobStore for backend
// tests. It mirrors the contract of *db.Pool but lives entirely in
// memory so the storage package can test its own behaviour without
// pulling in a real database.
type memChunkBlobStore struct {
	mu    sync.Mutex
	rows  map[string][]byte
}

func newMemChunkBlobStore() *memChunkBlobStore {
	return &memChunkBlobStore{rows: map[string][]byte{}}
}

func (m *memChunkBlobStore) UpsertChunkBlob(_ context.Context, key string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.rows[key]; exists {
		// emulate ON CONFLICT DO NOTHING — first writer wins.
		return nil
	}
	m.rows[key] = append([]byte(nil), data...)
	return nil
}

func (m *memChunkBlobStore) GetChunkBlob(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, ok := m.rows[key]
	if !ok {
		return nil, pgx.ErrNoRows
	}
	return append([]byte(nil), raw...), nil
}

func (m *memChunkBlobStore) DeleteChunkBlob(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, key)
	return nil
}

func (m *memChunkBlobStore) ChunkBlobExists(_ context.Context, key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.rows[key]
	return ok, nil
}

func TestPostgresBytea_PutComputesSha256(t *testing.T) {
	backend := NewPostgresBytea(newMemChunkBlobStore())
	payload := bytes.Repeat([]byte{0xab}, 4096)
	want := sha256.Sum256(payload)

	put, err := backend.Put(context.Background(), "k", bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)
	require.Equal(t, int64(len(payload)), put.Size)
	require.Equal(t, want[:], put.Sha256)
}

func TestPostgresBytea_PutRejectsLengthMismatch(t *testing.T) {
	backend := NewPostgresBytea(newMemChunkBlobStore())
	_, err := backend.Put(context.Background(), "k", bytes.NewReader([]byte{1, 2, 3}), 100)
	require.Error(t, err)
}

func TestPostgresBytea_RoundTrip(t *testing.T) {
	store := newMemChunkBlobStore()
	backend := NewPostgresBytea(store)
	payload := bytes.Repeat([]byte{0xcd}, 1024)

	_, err := backend.Put(context.Background(), "round", bytes.NewReader(payload), int64(len(payload)))
	require.NoError(t, err)

	exists, err := backend.Exists(context.Background(), "round")
	require.NoError(t, err)
	require.True(t, exists)

	reader, size, err := backend.Get(context.Background(), "round")
	require.NoError(t, err)
	defer reader.Close()
	require.Equal(t, int64(len(payload)), size)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, payload, got)

	require.NoError(t, backend.Delete(context.Background(), "round"))
	exists, err = backend.Exists(context.Background(), "round")
	require.NoError(t, err)
	require.False(t, exists)
}

func TestPostgresBytea_GetMissingReturnsErrNotFound(t *testing.T) {
	backend := NewPostgresBytea(newMemChunkBlobStore())
	_, _, err := backend.Get(context.Background(), "absent")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNotFound), "expected ErrNotFound, got %T %v", err, err)
}

func TestPostgresBytea_PresignReturnsInAPIURL(t *testing.T) {
	backend := NewPostgresBytea(newMemChunkBlobStore())
	put, err := backend.PresignPut(context.Background(), "arch-1/0", 0)
	require.NoError(t, err)
	require.Equal(t, "PUT", put.Method)
	require.Equal(t, "/api/auth/link-archive-chunk/arch-1/0", put.URL)

	get, err := backend.PresignGet(context.Background(), "arch-1/0", 0)
	require.NoError(t, err)
	require.Equal(t, "GET", get.Method)
	require.Equal(t, "/api/auth/link-archive-chunk/arch-1/0", get.URL)
}

func TestKindEnum(t *testing.T) {
	require.Equal(t, BackendPostgresBytea, NewPostgresBytea(newMemChunkBlobStore()).Kind())
}
