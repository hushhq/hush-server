package storage

import "testing"

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
