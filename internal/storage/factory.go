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
	Endpoint    string
	Region      string
	Bucket      string
	AccessKey   string
	SecretKey   string
	UseSSL      bool
}

// LoadConfig reads the relevant env vars and returns a Config. Defaults
// to BackendPostgresBytea so a self-host install with no extra config
// keeps working out of the box.
func LoadConfig() (Config, error) {
	kind := os.Getenv("STORAGE_BACKEND")
	if kind == "" {
		kind = string(BackendPostgresBytea)
	}
	switch BackendKind(kind) {
	case BackendPostgresBytea:
		return Config{Kind: BackendPostgresBytea}, nil
	case BackendS3:
		s3 := S3Config{
			Endpoint:  os.Getenv("STORAGE_S3_ENDPOINT"),
			Region:    os.Getenv("STORAGE_S3_REGION"),
			Bucket:    os.Getenv("STORAGE_S3_BUCKET"),
			AccessKey: os.Getenv("STORAGE_S3_ACCESS_KEY"),
			SecretKey: os.Getenv("STORAGE_S3_SECRET_KEY"),
			UseSSL:    os.Getenv("STORAGE_S3_USE_SSL") != "false",
		}
		if s3.Endpoint == "" || s3.Bucket == "" || s3.AccessKey == "" || s3.SecretKey == "" {
			return Config{}, errors.New("storage: STORAGE_S3_* env vars required when STORAGE_BACKEND=s3 (endpoint, bucket, access_key, secret_key)")
		}
		return Config{Kind: BackendS3, S3: s3}, nil
	default:
		return Config{}, fmt.Errorf("storage: unknown STORAGE_BACKEND %q", kind)
	}
}

// NewBackend constructs the configured Backend. The postgres_bytea
// path needs a ChunkBlobStore; the s3 path is intentionally not
// implemented in this slice — it exists in the abstraction so the API
// layer can be written against the interface today, with the
// production backend landing in the next session.
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
