package dbcompat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"

	"github.com/hushhq/hush-server/internal/version"
)

// fakeReader is a hand-rolled SchemaVersionReader for tests. Each field
// is returned verbatim from Version() so cases stay obvious.
type fakeReader struct {
	v     uint
	dirty bool
	err   error
}

func (f *fakeReader) Version() (uint, bool, error) {
	return f.v, f.dirty, f.err
}

// TestCheckSchemaCompatibility_FreshDB_ReturnsNoError covers the path
// where schema_migrations does not exist yet. golang-migrate returns
// migrate.ErrNilVersion in that case; the caller's m.Up() will create
// the table and apply every migration in the source tree, so boot
// must proceed.
func TestCheckSchemaCompatibility_FreshDB_ReturnsNoError(t *testing.T) {
	r := &fakeReader{err: migrate.ErrNilVersion}
	if err := CheckSchemaCompatibility(context.Background(), r); err != nil {
		t.Fatalf("fresh DB must allow boot, got: %v", err)
	}
}

// TestCheckSchemaCompatibility_DBAtCurrent_ReturnsNoError covers the
// common case: the binary just booted against a DB that is already
// at the highest migration this binary knows about. No-op is correct.
func TestCheckSchemaCompatibility_DBAtCurrent_ReturnsNoError(t *testing.T) {
	r := &fakeReader{v: uint(version.CurrentDBSchemaVersion)}
	if err := CheckSchemaCompatibility(context.Background(), r); err != nil {
		t.Fatalf("DB at CurrentDBSchemaVersion must allow boot, got: %v", err)
	}
}

// TestCheckSchemaCompatibility_DBBehindCurrent_ReturnsNoError covers
// the forward-migration path: a binary that ships a few new migrations
// boots against a DB that has not been upgraded yet. The check must
// not block; the caller's m.Up() is responsible for catching the DB
// up to the binary.
func TestCheckSchemaCompatibility_DBBehindCurrent_ReturnsNoError(t *testing.T) {
	if version.CurrentDBSchemaVersion <= 1 {
		t.Skipf("CurrentDBSchemaVersion (%d) too low to test behind-case", version.CurrentDBSchemaVersion)
	}
	r := &fakeReader{v: uint(version.CurrentDBSchemaVersion - 1)}
	if err := CheckSchemaCompatibility(context.Background(), r); err != nil {
		t.Fatalf("DB behind CurrentDBSchemaVersion must allow boot so m.Up() can catch it up, got: %v", err)
	}
}

// TestCheckSchemaCompatibility_DBAheadOfBinary_ReturnsActionableError
// is the central guard of HUSHHQ-83 phase 2. A self-hoster rolled
// back the container to an older release after a newer release had
// already migrated the schema; the older binary's code does not know
// the new shape. The check must refuse to start and the error must
// name both versions so the operator can decide between upgrading
// the binary and restoring from backup.
func TestCheckSchemaCompatibility_DBAheadOfBinary_ReturnsActionableError(t *testing.T) {
	dbVersion := uint(version.CurrentDBSchemaVersion + 5)
	r := &fakeReader{v: dbVersion}
	err := CheckSchemaCompatibility(context.Background(), r)
	if err == nil {
		t.Fatalf("DB ahead of binary must refuse to start, got nil error")
	}
	msg := err.Error()
	// Operator must see the live DB version, the binary's ceiling, and
	// at least one of the two remediation paths spelled out. We check
	// for "upgrade" and "backup" as concrete keywords from the guidance
	// so a future copy edit that strips the remediation steps fails the
	// test instead of silently shipping a less helpful error.
	for _, fragment := range []string{
		"v" + uitoa(dbVersion),
		"v" + uitoa(uint(version.CurrentDBSchemaVersion)),
		"upgrade",
		"backup",
		"Refusing to start",
	} {
		if !strings.Contains(msg, fragment) {
			t.Errorf("error message missing required fragment %q\nfull message: %s", fragment, msg)
		}
	}
}

// TestCheckSchemaCompatibility_DirtyDB_ReturnsActionableError
// covers a partial / failed migration. golang-migrate sets the dirty
// flag when an up- or down-migration aborted; running another up
// against a dirty schema is unsafe and the operator must intervene.
// The error must name the dirty version and tell them how to
// `migrate force` once they have verified the schema is sound.
func TestCheckSchemaCompatibility_DirtyDB_ReturnsActionableError(t *testing.T) {
	r := &fakeReader{v: 23, dirty: true}
	err := CheckSchemaCompatibility(context.Background(), r)
	if err == nil {
		t.Fatalf("dirty schema must refuse to start, got nil error")
	}
	msg := err.Error()
	for _, fragment := range []string{
		"dirty",
		"version 23",
		"migrate force 23",
		"Refusing to start",
	} {
		if !strings.Contains(msg, fragment) {
			t.Errorf("dirty error missing fragment %q\nfull message: %s", fragment, msg)
		}
	}
}

// TestCheckSchemaCompatibility_ReaderError_ReturnsWrappedError covers
// the path where the underlying Version() call fails for a reason
// other than ErrNilVersion. The error must propagate (wrapped) so the
// caller logs it and exits.
func TestCheckSchemaCompatibility_ReaderError_ReturnsWrappedError(t *testing.T) {
	rootCause := errors.New("connection reset by peer")
	r := &fakeReader{err: rootCause}
	err := CheckSchemaCompatibility(context.Background(), r)
	if err == nil {
		t.Fatalf("reader error must propagate, got nil error")
	}
	if !errors.Is(err, rootCause) {
		t.Errorf("error must wrap the underlying cause via errors.Is, got: %v", err)
	}
}

// TestCheckSchemaCompatibility_NilReader_ReturnsError documents the
// defensive guard against a misconfigured caller.
func TestCheckSchemaCompatibility_NilReader_ReturnsError(t *testing.T) {
	if err := CheckSchemaCompatibility(context.Background(), nil); err == nil {
		t.Fatalf("nil reader must return an error, got nil")
	}
}

// uitoa is a tiny helper used by the message-fragment assertion so the
// tests do not need to import strconv just to format two integers.
func uitoa(u uint) string {
	if u == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for u > 0 {
		i--
		b[i] = byte('0' + u%10)
		u /= 10
	}
	return string(b[i:])
}
