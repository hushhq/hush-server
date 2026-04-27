// Package storage defines the pluggable backend abstraction for archive
// chunk bytes used by the device-link transfer plane. The interface
// keeps the API and DB layers free of any direct knowledge of where
// chunk bytes live, so a self-host install can run on a Postgres-only
// stack and a production deployment can swap in S3-compatible object
// storage without changing call sites.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// BackendKind enumerates supported storage backends. The string form is
// what gets persisted in `link_archive_chunks.storage_backend` so each
// row remembers where its bytes were written. Adding a new backend
// requires extending the DB CHECK constraint as well.
type BackendKind string

const (
	BackendPostgresBytea BackendKind = "postgres_bytea"
	BackendS3            BackendKind = "s3"
)

// ErrNotFound is the canonical "no such object" error returned by every
// backend. Callers compare with errors.Is.
var ErrNotFound = errors.New("storage: object not found")

// PresignedURL describes a short-TTL credential that the client can use
// to talk to the backend directly. For backends that do not support
// presigning (notably postgres_bytea), URL points at the in-API endpoint
// that reads/writes through the abstraction; ContentSha256Header is
// non-empty only when the backend wants the client to advertise the
// content SHA-256 in a backend-native header (e.g. S3
// `x-amz-checksum-sha256`).
type PresignedURL struct {
	URL                  string
	Method               string
	Headers              map[string]string
	ExpiresAt            time.Time
	ContentSha256Header  string
}

// Backend is the contract every storage implementation must satisfy.
// All methods take a context for cancellation/timeout. Keys are opaque
// strings; the API layer is responsible for namespacing by archive id
// to avoid collisions.
type Backend interface {
	// Kind reports the persisted enum value for this backend.
	Kind() BackendKind

	// Put stores `size` bytes from r under key. Implementations must
	// compute SHA-256 internally where possible and return it for the
	// caller to verify against the client-declared hash before
	// confirming the chunk row.
	Put(ctx context.Context, key string, r io.Reader, size int64) (PutResult, error)

	// Get returns a reader for the bytes previously stored under key.
	// Caller must close the reader.
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)

	// Delete removes the object. Idempotent: no error when the object
	// did not exist (treated as ErrNotFound at the caller's discretion).
	Delete(ctx context.Context, key string) error

	// Exists is a cheap presence probe; backends should prefer HEAD
	// over GET. Used by the supervisor purger to verify deletes.
	Exists(ctx context.Context, key string) (bool, error)

	// PresignPut returns a presigned upload URL for the client to PUT
	// directly to. Backends without native presigning return a URL
	// pointing at an in-API fallback handler; see ChunkUploadFallback
	// in the api package.
	PresignPut(ctx context.Context, key string, ttl time.Duration) (PresignedURL, error)

	// PresignGet returns a presigned download URL.
	PresignGet(ctx context.Context, key string, ttl time.Duration) (PresignedURL, error)
}

// PutResult is what every Put call returns to the caller so the API
// layer can verify integrity and persist whatever metadata the chunk
// row needs.
type PutResult struct {
	// Sha256 is the SHA-256 digest of the bytes the backend actually
	// wrote. Backends that cannot compute this server-side return nil
	// and the API layer falls back to the client-declared digest.
	Sha256 []byte
	// Size is the number of bytes written.
	Size int64
}
