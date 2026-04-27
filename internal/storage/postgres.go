package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
)

// PostgresBytea persists chunk bytes inside the link_archive_chunks
// table itself. It is the default backend for self-host installs that
// have no object storage. Tests use it too because it requires zero
// external dependencies.
//
// Keys are opaque strings of the shape "<archive_id>/<idx>"; the
// implementation accepts any string and does not parse them — that
// shape is purely a convention enforced by the API layer.
type PostgresBytea struct {
	pool ChunkBlobStore
}

// ChunkBlobStore is the narrow DB surface PostgresBytea needs. Defined
// here (instead of importing internal/db) so the storage package stays
// import-free of the rest of the application and is trivially mockable
// in tests.
type ChunkBlobStore interface {
	UpsertChunkBlob(ctx context.Context, key string, bytes []byte) error
	GetChunkBlob(ctx context.Context, key string) ([]byte, error)
	DeleteChunkBlob(ctx context.Context, key string) error
	ChunkBlobExists(ctx context.Context, key string) (bool, error)
}

// NewPostgresBytea returns a PostgresBytea backend backed by store.
func NewPostgresBytea(store ChunkBlobStore) *PostgresBytea {
	return &PostgresBytea{pool: store}
}

// Kind reports the persisted enum value.
func (b *PostgresBytea) Kind() BackendKind { return BackendPostgresBytea }

// Put reads up to size bytes from r and writes them to the chunk_blobs
// table keyed by key. Computes SHA-256 in the process so the API layer
// can verify the client-declared hash before confirming the chunk row.
func (b *PostgresBytea) Put(ctx context.Context, key string, r io.Reader, size int64) (PutResult, error) {
	if size <= 0 {
		return PutResult{}, fmt.Errorf("postgres_bytea: size must be positive, got %d", size)
	}
	hasher := sha256.New()
	buf := bytes.NewBuffer(make([]byte, 0, size))
	tee := io.TeeReader(io.LimitReader(r, size+1), hasher)
	written, err := io.Copy(buf, tee)
	if err != nil {
		return PutResult{}, fmt.Errorf("postgres_bytea: read: %w", err)
	}
	if written != size {
		return PutResult{}, fmt.Errorf("postgres_bytea: stream length mismatch, want %d got %d", size, written)
	}
	if err := b.pool.UpsertChunkBlob(ctx, key, buf.Bytes()); err != nil {
		return PutResult{}, fmt.Errorf("postgres_bytea: upsert: %w", err)
	}
	return PutResult{Sha256: hasher.Sum(nil), Size: size}, nil
}

// Get returns a reader for the bytes previously stored under key.
func (b *PostgresBytea) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	raw, err := b.pool.GetChunkBlob(ctx, key)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("postgres_bytea: get: %w", err)
	}
	return io.NopCloser(bytes.NewReader(raw)), int64(len(raw)), nil
}

// Delete removes the row. Missing keys are treated as success because
// the operation is idempotent at the contract level.
func (b *PostgresBytea) Delete(ctx context.Context, key string) error {
	if err := b.pool.DeleteChunkBlob(ctx, key); err != nil {
		return fmt.Errorf("postgres_bytea: delete: %w", err)
	}
	return nil
}

// Exists is a cheap presence probe.
func (b *PostgresBytea) Exists(ctx context.Context, key string) (bool, error) {
	return b.pool.ChunkBlobExists(ctx, key)
}

// PresignPut for postgres_bytea returns a URL pointing at the in-API
// upload fallback handler. The API layer interprets PresignedURL.URL
// of the form "/api/auth/link-archive-chunk/{archiveId}/{idx}" as a
// signal to use the in-process upload endpoint and not a real S3 URL.
func (b *PostgresBytea) PresignPut(_ context.Context, key string, ttl time.Duration) (PresignedURL, error) {
	return PresignedURL{
		URL:       inAPIChunkURL(key),
		Method:    "PUT",
		Headers:   map[string]string{},
		ExpiresAt: time.Now().Add(ttl),
	}, nil
}

// PresignGet returns the in-API download fallback URL.
func (b *PostgresBytea) PresignGet(_ context.Context, key string, ttl time.Duration) (PresignedURL, error) {
	return PresignedURL{
		URL:       inAPIChunkURL(key),
		Method:    "GET",
		Headers:   map[string]string{},
		ExpiresAt: time.Now().Add(ttl),
	}, nil
}

// inAPIChunkURL converts an internal storage key of shape
// "<archive_id>/<idx>" into the public in-API endpoint URL. Anywhere
// the key shape changes, this conversion changes too.
func inAPIChunkURL(key string) string {
	return "/api/auth/link-archive-chunk/" + key
}

