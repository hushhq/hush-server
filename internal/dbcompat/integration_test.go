package dbcompat

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/hushhq/hush-server/internal/version"
)

// TestCheckSchemaCompatibility_Integration_DBAheadRefuses exercises the boot
// guardrail end to end against a real Postgres and a real *migrate.Migrate.
//
// This closes the HUSHHQ-83 phase 6 / HUSHHQ-89 live-test gap that the
// fake-reader unit tests cannot cover: it advances the live schema version
// past the binary's compiled-in ceiling and confirms CheckSchemaCompatibility
// refuses with the actionable error, then confirms the happy path at the
// binary's own version.
//
// Gated on DATABASE_URL (the env CI provides for the test job). Skips when
// unset so the unit suite still runs without a database. The test restores
// schema_migrations to the real highest version on exit; migrate.Force only
// rewrites the version row and never runs migration SQL, so the actual schema
// is left untouched.
func TestCheckSchemaCompatibility_Integration_DBAheadRefuses(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set; skipping guardrail integration test")
	}

	m := newTestMigrate(t, url)
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}
	// Always leave schema_migrations.version at the real highest migration,
	// no matter how this test exits, so a shared test database is not left in
	// an inconsistent state for other runs.
	defer func() {
		if err := m.Force(version.CurrentDBSchemaVersion); err != nil {
			t.Errorf("restore schema version to %d: %v", version.CurrentDBSchemaVersion, err)
		}
	}()

	ctx := context.Background()

	// Happy path: DB exactly at the binary's version must pass.
	if err := m.Force(version.CurrentDBSchemaVersion); err != nil {
		t.Fatalf("force to current version: %v", err)
	}
	if err := CheckSchemaCompatibility(ctx, m); err != nil {
		t.Fatalf("DB at CurrentDBSchemaVersion (%d) must pass, got: %v", version.CurrentDBSchemaVersion, err)
	}

	// The guard: DB one version past the binary's ceiling must refuse, with an
	// error that names both versions and the refusal so operator-facing logs
	// stay actionable.
	ahead := version.CurrentDBSchemaVersion + 1
	if err := m.Force(ahead); err != nil {
		t.Fatalf("force ahead to %d: %v", ahead, err)
	}
	err := CheckSchemaCompatibility(ctx, m)
	if err == nil {
		t.Fatal("DB ahead of binary must refuse to start, got nil error")
	}
	msg := err.Error()
	for _, frag := range []string{
		"v" + strconv.Itoa(ahead),
		"v" + strconv.Itoa(version.CurrentDBSchemaVersion),
		"Refusing to start",
	} {
		if !strings.Contains(msg, frag) {
			t.Errorf("guardrail error missing %q\nfull message: %s", frag, msg)
		}
	}
}

// newTestMigrate builds a *migrate.Migrate pointed at the repo's migrations
// directory (resolved relative to this test file) and the given database URL.
func newTestMigrate(t *testing.T, url string) *migrate.Migrate {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// internal/dbcompat -> repo root -> migrations
	dir := filepath.Join(wd, "..", "..", "migrations")
	if _, statErr := os.Stat(dir); statErr != nil {
		t.Fatalf("migrations dir not found at %s: %v", dir, statErr)
	}
	m, err := migrate.New("file://"+filepath.ToSlash(dir), url)
	if err != nil {
		t.Fatalf("migrate new: %v", err)
	}
	return m
}
