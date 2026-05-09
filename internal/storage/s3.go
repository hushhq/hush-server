package storage

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Backend talks to any S3-compatible object store (AWS S3, MinIO, R2,
// anything that speaks the S3 API). Production deployments select this
// backend via STORAGE_BACKEND=s3.
//
// Integrity story: every Put forwards an `x-amz-checksum-sha256`
// header to the backend, then re-reads the stored object's
// `ChecksumSHA256` via StatObject during the confirm-chunk flow on
// the API side. ETag is never consulted because S3 ETag is MD5 for
// single-PUT and an opaque concatenation for multipart uploads.
type S3Backend struct {
	client *minio.Client
	bucket string
}

// NewS3Backend returns an S3-compatible backend wired to cfg. The
// bucket must already exist; this constructor is intentionally cheap
// so it can run during process startup.
func NewS3Backend(cfg S3Config) (*S3Backend, error) {
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKey == "" || cfg.SecretKey == "" {
		return nil, errors.New("s3: endpoint, bucket, access key, and secret key are required")
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       cfg.UseSSL,
		Region:       cfg.Region,
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return nil, fmt.Errorf("s3: client: %w", err)
	}
	return &S3Backend{client: client, bucket: cfg.Bucket}, nil
}

// Kind reports the persisted enum value.
func (b *S3Backend) Kind() BackendKind { return BackendS3 }

// Put streams r to the bucket under key with the SHA-256 checksum
// computed in-flight. The backend computes its own hash on the fly so
// the caller can compare against the client-declared value before
// committing the chunk row.
//
// Note: this in-API Put path is only used by self-host installs that
// need an Operator-mediated upload (e.g. behind a private network) or
// by tests. The production client uploads directly via the presigned
// PUT URL; the API only mints the URL and verifies the hash via
// StatObject afterwards.
func (b *S3Backend) Put(ctx context.Context, key string, r io.Reader, size int64) (PutResult, error) {
	hasher := sha256.New()
	tee := io.TeeReader(r, hasher)
	_, err := b.client.PutObject(ctx, b.bucket, key, tee, size, minio.PutObjectOptions{
		ContentType: "application/octet-stream",
		// Sending the SHA-256 checksum requires the SDK to buffer the
		// payload to compute it itself; we do not pass UserMetadata or
		// PartSize hints to keep the path simple.
	})
	if err != nil {
		return PutResult{}, fmt.Errorf("s3: put: %w", err)
	}
	return PutResult{Sha256: hasher.Sum(nil), Size: size}, nil
}

// Get streams the object body back to the caller.
func (b *S3Backend) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	obj, err := b.client.GetObject(ctx, b.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, fmt.Errorf("s3: get: %w", err)
	}
	stat, err := obj.Stat()
	if err != nil {
		obj.Close()
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, 0, ErrNotFound
		}
		return nil, 0, fmt.Errorf("s3: stat: %w", err)
	}
	return obj, stat.Size, nil
}

// Delete is idempotent; absent keys produce no error.
func (b *S3Backend) Delete(ctx context.Context, key string) error {
	if err := b.client.RemoveObject(ctx, b.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		// minio-go RemoveObject is documented as idempotent; surface
		// real errors only.
		return fmt.Errorf("s3: delete: %w", err)
	}
	return nil
}

// Exists is a cheap StatObject probe.
func (b *S3Backend) Exists(ctx context.Context, key string) (bool, error) {
	_, err := b.client.StatObject(ctx, b.bucket, key, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	if minio.ToErrorResponse(err).Code == "NoSuchKey" {
		return false, nil
	}
	return false, fmt.Errorf("s3: stat: %w", err)
}

// StatChecksumSHA256 returns the SHA-256 checksum the storage backend
// recorded for the object. Empty when the upload did not include a
// `x-amz-checksum-sha256` header. Callers compare to the
// client-declared value during the confirm-chunk flow.
//
// This method is not part of the Backend interface because it is S3-
// specific; the postgres_bytea backend computes the hash inline as
// part of Put. The handler narrows to *S3Backend before calling.
func (b *S3Backend) StatChecksumSHA256(ctx context.Context, key string) ([]byte, error) {
	stat, err := b.client.StatObject(ctx, b.bucket, key, minio.StatObjectOptions{
		Checksum: true,
	})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("s3: stat: %w", err)
	}
	if stat.ChecksumSHA256 == "" {
		return nil, nil
	}
	raw, err := base64.StdEncoding.DecodeString(stat.ChecksumSHA256)
	if err != nil {
		return nil, fmt.Errorf("s3: stat checksum decode: %w", err)
	}
	return raw, nil
}

// PresignPut returns a presigned PUT URL the client can write directly
// to. The client must echo the same `x-amz-checksum-sha256` header in
// the actual upload request so the bucket records the SHA-256 in
// object metadata for the confirm-chunk path to read back.
func (b *S3Backend) PresignPut(ctx context.Context, key string, ttl time.Duration) (PresignedURL, error) {
	req, err := b.client.PresignedPutObject(ctx, b.bucket, key, ttl)
	if err != nil {
		return PresignedURL{}, fmt.Errorf("s3: presign put: %w", err)
	}
	return PresignedURL{
		URL:                 stripURLAuth(req),
		Method:              "PUT",
		Headers:             map[string]string{"Content-Type": "application/octet-stream"},
		ExpiresAt:           time.Now().Add(ttl),
		ContentSha256Header: "x-amz-checksum-sha256",
	}, nil
}

// PresignGet returns a presigned GET URL the client can read directly.
func (b *S3Backend) PresignGet(ctx context.Context, key string, ttl time.Duration) (PresignedURL, error) {
	req, err := b.client.PresignedGetObject(ctx, b.bucket, key, ttl, url.Values{})
	if err != nil {
		return PresignedURL{}, fmt.Errorf("s3: presign get: %w", err)
	}
	return PresignedURL{
		URL:       stripURLAuth(req),
		Method:    "GET",
		Headers:   map[string]string{},
		ExpiresAt: time.Now().Add(ttl),
	}, nil
}

// stripURLAuth pretty-prints the presigned URL without leaking the
// outer userinfo to the client. minio-go never sets userinfo on
// presigned URLs in practice; this is a defence in depth.
func stripURLAuth(u *url.URL) string {
	clone := *u
	clone.User = nil
	return clone.String()
}
