// Package migrationmeta parses and validates the per-migration metadata
// sidecars that accompany every SQL migration (HUSHHQ-83 phase 3).
//
// Every `NNNNNN_name.up.sql` in the migrations directory carries a sibling
// `NNNNNN_name.meta.json` declaring the compatibility facts a release and
// upgrade pipeline needs but cannot infer from the SQL itself:
//
//   - whether the migration is a hard compatibility break (a release that
//     carries one must never advance a moving tag like `latest`; HUSHHQ-84
//     consumes this),
//   - the minimum prior server version the migration assumes,
//   - whether a verified down-migration exists for rollback.
//
// The sidecar is JSON rather than an SQL comment so the release tooling and
// the boot-time guardrail can read it without parsing SQL, and so a stray
// edit to the SQL body cannot silently drop the metadata. A CI lint (the
// test in this package) fails the build when a migration is missing its
// sidecar, the version field disagrees with the filename, the
// supports_rollback flag disagrees with the on-disk down file, or the
// highest version drifts from version.CurrentDBSchemaVersion.
package migrationmeta

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
)

// Meta is the parsed content of a single `NNNNNN_name.meta.json` sidecar.
type Meta struct {
	// Version is the integer migration number, matching the NNNNNN prefix
	// of the migration filename.
	Version int `json:"version"`
	// Summary is a short human-readable description of the migration.
	Summary string `json:"summary"`
	// MinPriorServerVersion is the lowest server version this migration
	// assumes was already running. "0.0.0" means no floor is declared.
	MinPriorServerVersion string `json:"min_prior_server_version"`
	// CompatBreak marks a migration that is not safely reversible or that
	// changes a client/server/DB contract. A release shipping a
	// compat-break migration must not advance moving tags (HUSHHQ-84) and
	// should bump version.MinCompatibleDBSchemaVersion to this version.
	CompatBreak bool `json:"compat_break"`
	// SupportsRollback is true when a verified `.down.sql` exists for this
	// migration. The lint cross-checks it against the on-disk down file.
	SupportsRollback bool `json:"supports_rollback"`
}

// migrationFilePattern matches `NNNNNN_slug.up.sql` and captures the number
// and slug so the loader can pair up/down/meta files for the same migration.
var migrationFilePattern = regexp.MustCompile(`^(\d{6})_(.+)\.up\.sql$`)

// LoadAll parses and validates every migration metadata sidecar in dir.
//
// It returns the metadata sorted ascending by version, or the first
// validation error encountered. Validation covers, per migration:
//
//   - a `.meta.json` sidecar exists for every `.up.sql`,
//   - the sidecar parses as JSON with the expected shape,
//   - the `version` field equals the filename's NNNNNN number,
//   - `supports_rollback` agrees with whether a `.down.sql` exists,
//   - `min_prior_server_version` is non-empty,
//
// and across the set:
//
//   - versions are contiguous from 1..N with no gaps or duplicates.
//
// LoadAll does not consult version.CurrentDBSchemaVersion; the highest-
// version-matches-constant check lives in the package test so this function
// stays usable by release tooling that targets an arbitrary checkout.
func LoadAll(dir string) ([]Meta, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("migrationmeta: read dir %s: %w", dir, err)
	}

	var metas []Meta
	seen := make(map[int]string)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationFilePattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		num, _ := strconv.Atoi(m[1])
		slug := m[2]
		base := m[1] + "_" + slug

		if prior, dup := seen[num]; dup {
			return nil, fmt.Errorf("migrationmeta: duplicate migration version %d (%s and %s)", num, prior, e.Name())
		}
		seen[num] = e.Name()

		meta, err := loadOne(dir, base, num)
		if err != nil {
			return nil, err
		}
		metas = append(metas, meta)
	}

	if len(metas) == 0 {
		return nil, fmt.Errorf("migrationmeta: no migrations found in %s", dir)
	}

	sort.Slice(metas, func(i, j int) bool { return metas[i].Version < metas[j].Version })

	for i, meta := range metas {
		want := i + 1
		if meta.Version != want {
			return nil, fmt.Errorf("migrationmeta: non-contiguous versions: expected %d, found %d (%q)", want, meta.Version, meta.Summary)
		}
	}

	return metas, nil
}

// loadOne reads and validates a single migration's sidecar against its
// on-disk up/down files.
func loadOne(dir, base string, num int) (Meta, error) {
	metaPath := filepath.Join(dir, base+".meta.json")
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Meta{}, fmt.Errorf("migrationmeta: missing sidecar %s.meta.json (every migration needs one)", base)
		}
		return Meta{}, fmt.Errorf("migrationmeta: read %s: %w", metaPath, err)
	}

	var meta Meta
	if err := json.Unmarshal(raw, &meta); err != nil {
		return Meta{}, fmt.Errorf("migrationmeta: parse %s.meta.json: %w", base, err)
	}

	if meta.Version != num {
		return Meta{}, fmt.Errorf("migrationmeta: %s.meta.json version=%d disagrees with filename number %d", base, meta.Version, num)
	}
	if meta.MinPriorServerVersion == "" {
		return Meta{}, fmt.Errorf("migrationmeta: %s.meta.json has empty min_prior_server_version (use \"0.0.0\" for no floor)", base)
	}

	downExists := fileExists(filepath.Join(dir, base+".down.sql"))
	if meta.SupportsRollback != downExists {
		return Meta{}, fmt.Errorf(
			"migrationmeta: %s.meta.json supports_rollback=%v but .down.sql present=%v; they must agree",
			base, meta.SupportsRollback, downExists,
		)
	}

	return meta, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
