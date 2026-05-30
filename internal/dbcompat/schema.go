// Package dbcompat enforces the schema-vs-binary compatibility gate for
// self-hosted upgrades (HUSHHQ-83 phase 2).
//
// The hard rule: if `schema_migrations.version` in the live database is
// higher than the binary's compiled-in version.CurrentDBSchemaVersion,
// this binary refuses to start. The scenario it guards against is a
// rollback after a forward migration: a self-hoster pulls a newer
// release that migrates the schema to vN+k, then rolls the container
// back to the previous release (Watchtower, container restart loop on
// the older tag, etc). The older binary's code expects schema vN, but
// the DB now has columns or constraints from vN+k that the code does
// not understand. Letting it run produces silent data corruption.
//
// This package intentionally has no automatic recovery path. It does
// not attempt down-migrations, does not delete rows, does not strip
// columns. The operator is given an actionable error and must either
// upgrade the binary or restore the database from backup to a version
// the binary supports.
package dbcompat

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"

	"github.com/hushhq/hush-server/internal/version"
)

// refusalSuffix is the canonical trailer for every actionable error
// this package emits. Tests assert on the literal so a future copy
// edit that strips the suffix from one message but not the others
// fails the build instead of silently shipping inconsistent messages.
const refusalSuffix = "Refusing to start."

// SchemaVersionReader is the subset of *migrate.Migrate that this
// package consumes. Defined as an interface so tests can inject a
// fake without standing up a real Postgres or a migrate driver.
type SchemaVersionReader interface {
	// Version reports the current schema version, whether the
	// last migration left the table in a dirty state, and any
	// read error. It must return migrate.ErrNilVersion when the
	// schema_migrations table is empty (fresh database).
	Version() (uint, bool, error)
}

// Compile-time assertion that *migrate.Migrate satisfies the interface.
// If golang-migrate ever changes the signature of Version() this fails
// at build time rather than at boot.
var _ SchemaVersionReader = (*migrate.Migrate)(nil)

// CheckSchemaCompatibility verifies the live DB schema is reachable
// from this binary. It is meant to run after migrate.New() and before
// migrate.Up(): the up-migrations only know how to roll forward, so a
// DB ahead of the binary must be caught here.
//
// The check is intentionally narrow:
//
//   - migrate.ErrNilVersion (fresh DB, no migrations applied) is fine;
//     the caller's m.Up() will bring it up to current.
//   - A dirty schema_migrations row signals a partial migration. We
//     refuse to start so the operator can investigate (golang-migrate's
//     guidance is to manually FORCE the version after fixing the row).
//   - db_version > version.CurrentDBSchemaVersion is the rollback
//     scenario: refuse with an error that names both versions and
//     spells out the only two safe remediation paths.
//   - db_version <= version.CurrentDBSchemaVersion is OK. The forward
//     migration step (m.Up()) is the caller's responsibility and will
//     no-op when versions are equal.
//
// Returns nil when boot should proceed. Returns a non-nil error with
// an operator-actionable message when boot must abort. Callers should
// log the error and exit non-zero rather than retry or attempt any
// destructive recovery on their own.
//
// `ctx` is accepted on the signature so future SchemaVersionReader
// implementations that issue a real database query (e.g., a direct
// pgx call) can honor cancellation and deadlines without a breaking
// change. The current *migrate.Migrate path holds no I/O at this
// point in boot and ignores the value; tests rely on that, so the
// underscore-import style is deliberate.
func CheckSchemaCompatibility(_ context.Context, r SchemaVersionReader) error {
	if r == nil {
		return errors.New("dbcompat: nil SchemaVersionReader")
	}
	dbVersion, dirty, err := r.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		// Fresh database. m.Up() will create schema_migrations and
		// apply every migration in the source tree.
		return nil
	}
	if err != nil {
		return fmt.Errorf("dbcompat: read schema_migrations.version: %w", err)
	}
	if dirty {
		return dirtySchemaError(dbVersion)
	}
	if dbVersion > uint(version.CurrentDBSchemaVersion) {
		return aheadOfBinaryError(dbVersion)
	}
	return nil
}

// dirtySchemaError formats the actionable error returned when
// schema_migrations.dirty is true. Splitting the message off the
// main check keeps CheckSchemaCompatibility under the function-length
// limit and lets the test suite assert against a single message
// builder rather than a printf inside a control-flow branch.
func dirtySchemaError(dbVersion uint) error {
	return fmt.Errorf(
		"dbcompat: schema_migrations is dirty at version %d. "+
			"A previous migration did not complete cleanly. Investigate the "+
			"failed migration, restore from backup if necessary, then either "+
			"resolve the partial state manually or use `migrate force %d` once "+
			"you have verified the schema actually matches that version. %s",
		dbVersion, dbVersion, refusalSuffix,
	)
}

// aheadOfBinaryError formats the actionable error returned when the
// live DB schema has been migrated past this binary's compiled-in
// ceiling. The message names both versions and spells out the only
// two safe remediation paths.
func aheadOfBinaryError(dbVersion uint) error {
	return fmt.Errorf(
		"dbcompat: database schema_migrations.version is at v%d, but this "+
			"binary supports up to v%d. The database was likely migrated by a "+
			"newer hush-server release than the one you are starting now (a "+
			"common cause is rolling back a container while keeping the "+
			"upgraded database). Two safe options: (a) upgrade the binary to a "+
			"release that supports schema v%d or higher, or (b) restore the "+
			"database from a backup taken before the upgrade. %s "+
			"This prevents silent data corruption.",
		dbVersion, version.CurrentDBSchemaVersion, dbVersion, refusalSuffix,
	)
}
