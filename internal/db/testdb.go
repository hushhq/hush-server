package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

const testDBEnvVar = "TEST_DATABASE_URL"

// SetupTestDB runs migrations on the test database and returns a pool and cleanup function.
// Skips the test if TEST_DATABASE_URL is not set so unit tests without a DB still run.
// Caller should defer cleanup() to close the pool.
// For repeatable tests, truncate tables at the start of the test (see TestPool_Integration).
func SetupTestDB(t *testing.T) (*Pool, func()) {
	t.Helper()
	url := os.Getenv(testDBEnvVar)
	if url == "" {
		t.Skipf("%s not set, skipping integration test", testDBEnvVar)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	migrationsDir := filepath.Join(wd, "migrations")
	if _, err := os.Stat(migrationsDir); os.IsNotExist(err) {
		migrationsDir = filepath.Join(wd, "server", "migrations")
	}
	migrationsPath := "file://" + filepath.ToSlash(migrationsDir)
	m, err := migrate.New(migrationsPath, url)
	if err != nil {
		t.Fatalf("migrate new: %v", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}

	ctx := context.Background()
	pool, err := Open(ctx, url)
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	return pool, func() { pool.Close() }
}
