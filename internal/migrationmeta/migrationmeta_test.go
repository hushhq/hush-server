package migrationmeta

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hushhq/hush-server/internal/version"
)

// migrationsDir resolves the repository's migrations/ directory relative to
// this test file, so the lint runs whether `go test` is invoked from the
// package directory or the repo root.
func migrationsDir(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}
	// internal/migrationmeta -> repo root -> migrations
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("migrations dir not found at %s", dir)
	}
	return dir
}

// TestLoadAll_RealMigrations_Validates is the CI lint for the migration
// metadata sidecars. It fails the build when any migration is missing its
// sidecar, the sidecar disagrees with its filename, supports_rollback
// disagrees with the on-disk down file, or versions are non-contiguous.
func TestLoadAll_RealMigrations_Validates(t *testing.T) {
	metas, err := LoadAll(migrationsDir(t))
	if err != nil {
		t.Fatalf("migration metadata lint failed: %v", err)
	}
	if len(metas) == 0 {
		t.Fatal("expected at least one migration")
	}
}

// TestHighestVersion_MatchesCurrentDBSchemaVersion enforces the invariant the
// HACK note in version.go used to carry by hand: the compiled-in
// CurrentDBSchemaVersion must equal the highest migration on disk. This is
// the build-time check that lets phase 2's boot guardrail be trusted.
func TestHighestVersion_MatchesCurrentDBSchemaVersion(t *testing.T) {
	metas, err := LoadAll(migrationsDir(t))
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	highest := metas[len(metas)-1].Version
	if highest != version.CurrentDBSchemaVersion {
		t.Errorf("highest migration version (%d) != version.CurrentDBSchemaVersion (%d); bump the constant when adding a migration",
			highest, version.CurrentDBSchemaVersion)
	}
}

// TestMinCompatibleDBSchema_ConsistentWithCompatBreaks verifies that the
// declared MinCompatibleDBSchemaVersion is not below the highest compat-break
// migration. A compat break marks every prior schema unsupported, so the
// binary's minimum compatible schema must be at least that version. With no
// compat breaks declared, any value <= Current is allowed.
func TestMinCompatibleDBSchema_ConsistentWithCompatBreaks(t *testing.T) {
	metas, err := LoadAll(migrationsDir(t))
	if err != nil {
		t.Fatalf("load migrations: %v", err)
	}
	highestBreak := 0
	for _, m := range metas {
		if m.CompatBreak && m.Version > highestBreak {
			highestBreak = m.Version
		}
	}
	if highestBreak > version.MinCompatibleDBSchemaVersion {
		t.Errorf("highest compat_break migration is v%d but MinCompatibleDBSchemaVersion is %d; a compat break must raise the minimum compatible schema to at least its own version",
			highestBreak, version.MinCompatibleDBSchemaVersion)
	}
}

// TestLoadAll_MissingSidecar_Fails proves the lint actually catches a missing
// sidecar by running LoadAll against a temp dir with an up.sql but no
// meta.json.
func TestLoadAll_MissingSidecar_Fails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "000001_init.up.sql", "SELECT 1;")
	writeFile(t, dir, "000001_init.down.sql", "SELECT 1;")
	if _, err := LoadAll(dir); err == nil {
		t.Fatal("expected error for missing .meta.json, got nil")
	}
}

// TestLoadAll_VersionMismatch_Fails proves the lint catches a sidecar whose
// version field disagrees with the filename number.
func TestLoadAll_VersionMismatch_Fails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "000001_init.up.sql", "SELECT 1;")
	writeFile(t, dir, "000001_init.down.sql", "SELECT 1;")
	writeMeta(t, dir, "000001_init.meta.json", Meta{
		Version: 2, Summary: "init", MinPriorServerVersion: "0.0.0",
		CompatBreak: false, SupportsRollback: true,
	})
	if _, err := LoadAll(dir); err == nil {
		t.Fatal("expected error for version mismatch, got nil")
	}
}

// TestLoadAll_RollbackFlagMismatch_Fails proves the lint catches a sidecar
// that claims supports_rollback=true with no .down.sql on disk.
func TestLoadAll_RollbackFlagMismatch_Fails(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "000001_init.up.sql", "SELECT 1;")
	// No .down.sql written.
	writeMeta(t, dir, "000001_init.meta.json", Meta{
		Version: 1, Summary: "init", MinPriorServerVersion: "0.0.0",
		CompatBreak: false, SupportsRollback: true,
	})
	if _, err := LoadAll(dir); err == nil {
		t.Fatal("expected error for supports_rollback/down-file mismatch, got nil")
	}
}

// TestLoadAll_NonContiguous_Fails proves the lint catches a gap in the
// migration version sequence.
func TestLoadAll_NonContiguous_Fails(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"000001_a", "000003_c"} {
		writeFile(t, dir, n+".up.sql", "SELECT 1;")
		writeFile(t, dir, n+".down.sql", "SELECT 1;")
	}
	writeMeta(t, dir, "000001_a.meta.json", Meta{Version: 1, Summary: "a", MinPriorServerVersion: "0.0.0", SupportsRollback: true})
	writeMeta(t, dir, "000003_c.meta.json", Meta{Version: 3, Summary: "c", MinPriorServerVersion: "0.0.0", SupportsRollback: true})
	if _, err := LoadAll(dir); err == nil {
		t.Fatal("expected error for non-contiguous versions, got nil")
	}
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func writeMeta(t *testing.T, dir, name string, m Meta) {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal meta: %v", err)
	}
	writeFile(t, dir, name, string(raw))
}
