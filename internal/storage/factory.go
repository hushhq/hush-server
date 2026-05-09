package storage

import (
	"errors"
	"fmt"
	"os"
)

// Config captures the env-derived storage configuration. Resolve from
// the process environment with LoadConfig and pass to NewBackend.
type Config struct {
	// Kind is the persisted-enum value of the selected backend.
	Kind BackendKind
	// S3 is populated when Kind == BackendS3.
	S3 S3Config
}

// S3Config holds the connection parameters for an S3-compatible
// backend. Bucket must already exist; this layer does not create it.
type S3Config struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

const (
	storageBackendEnv     = "STORAGE_BACKEND"
	storageS3EndpointEnv  = "STORAGE_S3_ENDPOINT"
	storageS3RegionEnv    = "STORAGE_S3_REGION"
	storageS3BucketEnv    = "STORAGE_S3_BUCKET"
	storageS3AccessKeyEnv = "STORAGE_S3_ACCESS_KEY"
	storageS3SecretKeyEnv = "STORAGE_S3_SECRET_KEY"
	storageS3UseSSLEnv    = "STORAGE_S3_USE_SSL"

	attachmentStorageBackendEnv     = "ATTACHMENT_STORAGE_BACKEND"
	attachmentStorageS3EndpointEnv  = "ATTACHMENT_STORAGE_S3_ENDPOINT"
	attachmentStorageS3RegionEnv    = "ATTACHMENT_STORAGE_S3_REGION"
	attachmentStorageS3BucketEnv    = "ATTACHMENT_STORAGE_S3_BUCKET"
	attachmentStorageS3AccessKeyEnv = "ATTACHMENT_STORAGE_S3_ACCESS_KEY"
	attachmentStorageS3SecretKeyEnv = "ATTACHMENT_STORAGE_S3_SECRET_KEY"
	attachmentStorageS3UseSSLEnv    = "ATTACHMENT_STORAGE_S3_USE_SSL"
)

// LoadConfig reads the relevant env vars and returns a Config. Defaults
// to BackendPostgresBytea so a self-host install with no extra config
// keeps working out of the box.
func LoadConfig() (Config, error) {
	return loadConfigFromEnv(envReader{}, storageEnvNames{
		Backend:   storageBackendEnv,
		Endpoint:  storageS3EndpointEnv,
		Region:    storageS3RegionEnv,
		Bucket:    storageS3BucketEnv,
		AccessKey: storageS3AccessKeyEnv,
		SecretKey: storageS3SecretKeyEnv,
		UseSSL:    storageS3UseSSLEnv,
	})
}

// LoadAttachmentConfig reads the attachment-specific storage env vars
// and falls back to the general STORAGE_* block when no attachment
// override is present. This keeps older installs working while allowing
// chat attachments to live in a bucket separate from device-link archives.
func LoadAttachmentConfig() (Config, error) {
	return loadAttachmentConfigFromEnv(envReader{})
}

type storageEnvNames struct {
	Backend   string
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	UseSSL    string
}

type envReader struct{}

func (envReader) Get(key string) string { return os.Getenv(key) }

type stringGetter interface {
	Get(key string) string
}

func loadAttachmentConfigFromEnv(env stringGetter) (Config, error) {
	attachment := storageEnvNames{
		Backend:   attachmentStorageBackendEnv,
		Endpoint:  attachmentStorageS3EndpointEnv,
		Region:    attachmentStorageS3RegionEnv,
		Bucket:    attachmentStorageS3BucketEnv,
		AccessKey: attachmentStorageS3AccessKeyEnv,
		SecretKey: attachmentStorageS3SecretKeyEnv,
		UseSSL:    attachmentStorageS3UseSSLEnv,
	}
	base := storageEnvNames{
		Backend:   storageBackendEnv,
		Endpoint:  storageS3EndpointEnv,
		Region:    storageS3RegionEnv,
		Bucket:    storageS3BucketEnv,
		AccessKey: storageS3AccessKeyEnv,
		SecretKey: storageS3SecretKeyEnv,
		UseSSL:    storageS3UseSSLEnv,
	}
	return loadConfigFromValues(
		coalesce(env, attachment.Backend, base.Backend),
		S3Config{
			Endpoint:  coalesce(env, attachment.Endpoint, base.Endpoint),
			Region:    coalesce(env, attachment.Region, base.Region),
			Bucket:    coalesce(env, attachment.Bucket, base.Bucket),
			AccessKey: coalesce(env, attachment.AccessKey, base.AccessKey),
			SecretKey: coalesce(env, attachment.SecretKey, base.SecretKey),
			UseSSL:    coalesce(env, attachment.UseSSL, base.UseSSL) != "false",
		},
	)
}

func loadConfigFromEnv(env stringGetter, names storageEnvNames) (Config, error) {
	return loadConfigFromValues(env.Get(names.Backend), S3Config{
		Endpoint:  env.Get(names.Endpoint),
		Region:    env.Get(names.Region),
		Bucket:    env.Get(names.Bucket),
		AccessKey: env.Get(names.AccessKey),
		SecretKey: env.Get(names.SecretKey),
		UseSSL:    env.Get(names.UseSSL) != "false",
	})
}

func loadConfigFromValues(kind string, s3 S3Config) (Config, error) {
	if kind == "" {
		kind = string(BackendPostgresBytea)
	}
	switch BackendKind(kind) {
	case BackendPostgresBytea:
		return Config{Kind: BackendPostgresBytea}, nil
	case BackendS3:
		if s3.Endpoint == "" || s3.Bucket == "" || s3.AccessKey == "" || s3.SecretKey == "" {
			return Config{}, errors.New("storage: STORAGE_S3_* env vars required when STORAGE_BACKEND=s3 (endpoint, bucket, access_key, secret_key)")
		}
		return Config{Kind: BackendS3, S3: s3}, nil
	default:
		return Config{}, fmt.Errorf("storage: unknown STORAGE_BACKEND %q", kind)
	}
}

func coalesce(env stringGetter, primary string, fallback string) string {
	if value := env.Get(primary); value != "" {
		return value
	}
	return env.Get(fallback)
}

// NewBackend constructs the configured Backend. The postgres_bytea path
// needs a ChunkBlobStore; the s3 path uses the configured compatible
// object store directly.
func NewBackend(cfg Config, postgresStore ChunkBlobStore) (Backend, error) {
	switch cfg.Kind {
	case BackendPostgresBytea:
		if postgresStore == nil {
			return nil, errors.New("storage: postgres_bytea backend requires a non-nil ChunkBlobStore")
		}
		return NewPostgresBytea(postgresStore), nil
	case BackendS3:
		return NewS3Backend(cfg.S3)
	default:
		return nil, fmt.Errorf("storage: unknown backend kind %q", cfg.Kind)
	}
}
