package storage

import (
	"context"
	"errors"
	"testing"
)

type stubChunkStore struct{}

func (stubChunkStore) UpsertChunkBlob(context.Context, string, []byte) error { return nil }
func (stubChunkStore) GetChunkBlob(context.Context, string) ([]byte, error)  { return nil, nil }
func (stubChunkStore) DeleteChunkBlob(context.Context, string) error         { return nil }
func (stubChunkStore) ChunkBlobExists(context.Context, string) (bool, error) { return false, nil }

type mapEnv map[string]string

func (m mapEnv) Get(key string) string { return m[key] }

func TestLoadAttachmentConfig_UsesDedicatedAttachmentBucket(t *testing.T) {
	cfg, err := loadAttachmentConfigFromEnv(mapEnv{
		storageBackendEnv:              string(BackendS3),
		storageS3EndpointEnv:           "app.gethush.live",
		storageS3RegionEnv:             "us-east-1",
		storageS3BucketEnv:             "hush-link-archive",
		storageS3AccessKeyEnv:          "minio-user",
		storageS3SecretKeyEnv:          "minio-secret",
		storageS3UseSSLEnv:             "true",
		attachmentStorageS3BucketEnv:   "hush-attachments",
		attachmentStorageS3UseSSLEnv:   "true",
		attachmentStorageS3EndpointEnv: "",
	})
	if err != nil {
		t.Fatalf("load attachment config: %v", err)
	}
	if cfg.Kind != BackendS3 {
		t.Fatalf("kind = %q, want %q", cfg.Kind, BackendS3)
	}
	if cfg.S3.Bucket != "hush-attachments" {
		t.Fatalf("bucket = %q, want hush-attachments", cfg.S3.Bucket)
	}
	if cfg.S3.Endpoint != "app.gethush.live" {
		t.Fatalf("endpoint = %q, want app.gethush.live", cfg.S3.Endpoint)
	}
}

// TestNewAttachmentBackendFactory_AcceptsPostgresBytea pins that the
// production attachment factory does not reject the postgres_bytea
// backend — the regression Codex caught after the first review pass.
// The API handler then routes blob traffic through the authenticated
// in-API `PUT/GET /api/attachments/{id}/blob` fallback.
func TestNewAttachmentBackendFactory_AcceptsPostgresBytea(t *testing.T) {
	factory := newAttachmentBackendFactory(stubChunkStore{}, func() (Config, error) {
		return Config{Kind: BackendPostgresBytea}, nil
	})
	backend, err := factory()
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if backend == nil {
		t.Fatal("factory returned nil backend for postgres_bytea")
	}
	if backend.Kind() != BackendPostgresBytea {
		t.Fatalf("kind = %q, want %q", backend.Kind(), BackendPostgresBytea)
	}
}

// TestNewAttachmentBackendFactory_AcceptsS3 confirms the S3 path also
// keeps working through the new factory seam. Uses fabricated env-shaped
// values so no real network or credentials are involved.
func TestNewAttachmentBackendFactory_AcceptsS3(t *testing.T) {
	factory := newAttachmentBackendFactory(stubChunkStore{}, func() (Config, error) {
		return Config{
			Kind: BackendS3,
			S3: S3Config{
				Endpoint:  "storage.example.com",
				Region:    "us-east-1",
				Bucket:    "hush-attachments",
				AccessKey: "k",
				SecretKey: "s",
				UseSSL:    true,
			},
		}, nil
	})
	backend, err := factory()
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if backend == nil {
		t.Fatal("factory returned nil backend for s3")
	}
	if backend.Kind() != BackendS3 {
		t.Fatalf("kind = %q, want %q", backend.Kind(), BackendS3)
	}
}

// TestNewAttachmentBackendFactory_PropagatesLoaderError ensures a
// transient config-load failure is surfaced to the caller instead of
// being papered over with a nil backend.
func TestNewAttachmentBackendFactory_PropagatesLoaderError(t *testing.T) {
	want := errors.New("env unreadable")
	factory := newAttachmentBackendFactory(stubChunkStore{}, func() (Config, error) {
		return Config{}, want
	})
	backend, err := factory()
	if backend != nil {
		t.Fatalf("backend = %v, want nil on loader error", backend)
	}
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

func TestLoadAttachmentConfig_FallsBackToGeneralStorageBlock(t *testing.T) {
	cfg, err := loadAttachmentConfigFromEnv(mapEnv{
		storageBackendEnv:     string(BackendS3),
		storageS3EndpointEnv:  "storage.example.com",
		storageS3RegionEnv:    "us-east-1",
		storageS3BucketEnv:    "hush-link-archive",
		storageS3AccessKeyEnv: "minio-user",
		storageS3SecretKeyEnv: "minio-secret",
		storageS3UseSSLEnv:    "false",
	})
	if err != nil {
		t.Fatalf("load attachment config: %v", err)
	}
	if cfg.S3.Bucket != "hush-link-archive" {
		t.Fatalf("bucket = %q, want hush-link-archive", cfg.S3.Bucket)
	}
	if cfg.S3.UseSSL {
		t.Fatal("UseSSL = true, want false")
	}
}
