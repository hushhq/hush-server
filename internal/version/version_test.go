// Tests for the compat envelope constants exposed via internal/version.
//
// These run as part of `go test ./...` and catch the silent-drift class
// of bug where a server-side constant disagrees with a downstream
// artifact (a sibling repo file, a migrations directory) without anyone
// noticing until production handshakes start refusing connections.
package version

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestCryptoCompatRanges_MatchesHushWebPin verifies that the server's
// authoritative crypto compat envelope agrees with the version pin in
// `hush-web/compatibility.json`.
//
// In a developer monorepo checkout the two repos sit next to each other
// at /Users/.../hush/{hush-server,hush-web} (or similar). When the test
// can find the file relative to the test source directory, it parses it
// and asserts every (package, version) pair declared in
// `hush-web/compatibility.json` -> `requires` matches the corresponding
// entry in version.CryptoCompatRanges. Drift fails the test loudly.
//
// In a CI build where only hush-server is checked out, the file does not
// exist; the test skips with a clear note rather than fail. The lint we
// land in HUSHHQ-83 phase 3 will close the CI gap for cross-repo coupling.
func TestCryptoCompatRanges_MatchesHushWebPin(t *testing.T) {
	hushWebCompatPath := findHushWebCompatibilityJSON(t)
	if hushWebCompatPath == "" {
		t.Skipf("hush-web/compatibility.json not on disk; skipping cross-repo drift check")
		return
	}
	raw, err := os.ReadFile(hushWebCompatPath)
	if err != nil {
		t.Fatalf("read hush-web compatibility.json at %s: %v", hushWebCompatPath, err)
	}
	var parsed struct {
		Requires map[string]string `json:"requires"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse hush-web compatibility.json: %v", err)
	}
	if len(parsed.Requires) == 0 {
		t.Fatalf("hush-web compatibility.json has no `requires` entries; cannot validate drift")
	}
	for pkg, want := range parsed.Requires {
		got, ok := CryptoCompatRanges[pkg]
		if !ok {
			t.Errorf("hush-web pins %q at %q but server CryptoCompatRanges does not declare it; phase 1 envelope drift", pkg, want)
			continue
		}
		if got != want {
			t.Errorf("crypto compat drift for %q: hush-web pin=%q, server CryptoCompatRanges=%q. Bump version.CryptoCompatRanges to match (or coordinate the bump cross-repo).", pkg, want, got)
		}
	}
}

// TestCryptoCompatRanges_IsNonEmpty guards against an accidental wipe
// of the envelope. The map is server-authoritative; an empty value
// would silently disable the phase 4 client gate.
func TestCryptoCompatRanges_IsNonEmpty(t *testing.T) {
	if len(CryptoCompatRanges) == 0 {
		t.Fatalf("CryptoCompatRanges must declare at least one entry; an empty envelope disables the phase 4 client gate")
	}
	for pkg, ver := range CryptoCompatRanges {
		if pkg == "" {
			t.Errorf("CryptoCompatRanges contains an empty package key with value %q", ver)
		}
		if ver == "" {
			t.Errorf("CryptoCompatRanges entry for %q is an empty version constraint", pkg)
		}
	}
}

// TestDBSchemaVersionBounds verifies the boot-time guardrail invariant
// (HUSHHQ-83 phase 2 consumes this) that the minimum compatible schema
// is never higher than the current schema. A violation would mean every
// fresh deploy refuses to start.
func TestDBSchemaVersionBounds(t *testing.T) {
	if MinCompatibleDBSchemaVersion > CurrentDBSchemaVersion {
		t.Fatalf("MinCompatibleDBSchemaVersion (%d) must be <= CurrentDBSchemaVersion (%d); a higher minimum locks every binary out of every database it ships against",
			MinCompatibleDBSchemaVersion, CurrentDBSchemaVersion)
	}
	if CurrentDBSchemaVersion <= 0 {
		t.Fatalf("CurrentDBSchemaVersion must be positive, got %d", CurrentDBSchemaVersion)
	}
}

// The CurrentDBSchemaVersion-equals-highest-migration invariant moved to the
// HUSHHQ-83 phase 3 lint in internal/migrationmeta
// (TestHighestVersion_MatchesCurrentDBSchemaVersion), which also validates the
// per-migration metadata sidecars. It is intentionally not duplicated here.

// findHushWebCompatibilityJSON locates hush-web/compatibility.json relative
// to this file's source location, walking up from internal/version/ until
// a sibling `hush-web/compatibility.json` is found. Returns "" if missing.
// Used by the cross-repo drift test so it works for both monorepo dev
// checkouts and CI builds that have only hush-server.
func findHushWebCompatibilityJSON(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	dir := filepath.Dir(thisFile)
	// Walk up at most 6 levels to find a sibling `hush-web` directory.
	for i := 0; i < 6; i++ {
		candidate := filepath.Join(dir, "..", "hush-web", "compatibility.json")
		if _, err := os.Stat(candidate); err == nil {
			abs, err := filepath.Abs(candidate)
			if err != nil {
				return candidate
			}
			return abs
		} else if !errors.Is(err, fs.ErrNotExist) {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
	return ""
}
