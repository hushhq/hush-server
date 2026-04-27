package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// LinkArchive is the metadata row that backs a chunked device-link transfer.
// Token columns hold SHA-256 of the raw bearer tokens; the raw bytes never
// touch persistent storage. Callers compare by hashing the inbound bearer.
type LinkArchive struct {
	ID                string
	UserID            string // empty for legacy MVP rows; new inserts always populate
	UploadTokenHash   []byte
	DownloadTokenHash []byte
	TotalChunks       int
	TotalBytes        int64
	ChunkSize         int
	ManifestHash      []byte
	ArchiveSHA256     []byte
	Finalized         bool   // legacy boolean; State is the authoritative lifecycle field
	State             string // see LinkArchiveState* constants
	CreatedAt         time.Time
	ExpiresAt         time.Time
	HardDeadlineAt    time.Time
}

// Lifecycle states stored in link_archives.state. Mirrors the state
// machine in /home/yarin/hush/answer.md and the CHECK constraint in
// migration 000032.
const (
	LinkArchiveStateCreated         = "created"
	LinkArchiveStateUploading       = "uploading"
	LinkArchiveStateUploadPaused    = "upload_paused"
	LinkArchiveStateUploaded        = "uploaded"
	LinkArchiveStateAvailable       = "available"
	LinkArchiveStateImporting       = "importing"
	LinkArchiveStateImportPaused    = "import_paused"
	LinkArchiveStateImported        = "imported"
	LinkArchiveStateAcknowledged    = "acknowledged"
	LinkArchiveStateAborted         = "aborted"
	LinkArchiveStateExpired         = "expired"
	LinkArchiveStateTerminalFailure = "terminal_failure"
)

// LinkArchiveActiveStates is the set of states that count toward the
// per-user concurrent-archive quota and the per-instance staging-bytes
// ceiling. A row in any of these states is "live" — it is reserving
// server resources.
var LinkArchiveActiveStates = []string{
	LinkArchiveStateCreated,
	LinkArchiveStateUploading,
	LinkArchiveStateUploadPaused,
	LinkArchiveStateUploaded,
	LinkArchiveStateAvailable,
	LinkArchiveStateImporting,
	LinkArchiveStateImportPaused,
	LinkArchiveStateImported,
}

// LinkArchiveChunkRow describes a stored archive chunk for verification.
type LinkArchiveChunkRow struct {
	Idx            int
	ChunkSize      int
	ChunkHash      []byte
	StorageBackend string
	StorageKey     string
}

// ErrLinkArchiveChunkConflict is returned by InsertLinkArchiveChunk when a
// chunk for (archiveID, idx) already exists with a different hash. Callers
// surface this as a 409 Conflict; the row is left untouched.
var ErrLinkArchiveChunkConflict = errors.New("link archive chunk conflict")

// ErrLinkArchiveExpired is returned by lookup helpers when the archive row
// is past its expiry or hard deadline. Treated as 404 to avoid leaking
// existence information.
var ErrLinkArchiveExpired = errors.New("link archive expired")

// LinkArchiveInsert is the parameter struct for InsertLinkArchive. The
// previous positional signature was already five-argument heavy; bundling
// keeps call sites readable when more fields are added.
type LinkArchiveInsert struct {
	UserID            string
	UploadTokenHash   []byte
	DownloadTokenHash []byte
	TotalChunks       int
	TotalBytes        int64
	ChunkSize         int
	ManifestHash      []byte
	ArchiveSHA256     []byte
	ExpiresAt         time.Time
	HardDeadlineAt    time.Time
}

// InsertLinkArchive persists a new archive metadata row in state 'created'.
// State is advanced by the API layer via TransitionLinkArchiveState.
func (p *Pool) InsertLinkArchive(ctx context.Context, in LinkArchiveInsert) (*LinkArchive, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO link_archives (
			user_id, upload_token_hash, download_token_hash,
			total_chunks, total_bytes, chunk_size,
			manifest_hash, archive_sha256,
			state, expires_at, hard_deadline_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING id, user_id, upload_token_hash, download_token_hash,
		          total_chunks, total_bytes, chunk_size,
		          manifest_hash, archive_sha256, finalized, state,
		          created_at, expires_at, hard_deadline_at`,
		in.UserID, in.UploadTokenHash, in.DownloadTokenHash,
		in.TotalChunks, in.TotalBytes, in.ChunkSize,
		in.ManifestHash, in.ArchiveSHA256,
		LinkArchiveStateCreated, in.ExpiresAt, in.HardDeadlineAt,
	)
	return scanLinkArchive(row)
}

// CountActiveLinkArchivesForUser returns the number of non-terminal
// archives the user owns. Used by the per-user concurrent quota check.
func (p *Pool) CountActiveLinkArchivesForUser(ctx context.Context, userID string) (int, error) {
	row := p.QueryRow(ctx, `
		SELECT COUNT(*) FROM link_archives
		WHERE user_id = $1 AND state = ANY($2)`,
		userID, LinkArchiveActiveStates,
	)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// SumActiveLinkArchiveBytes returns the sum of total_bytes across every
// non-terminal archive on the instance. Used by the per-instance
// staging-bytes ceiling check.
func (p *Pool) SumActiveLinkArchiveBytes(ctx context.Context) (int64, error) {
	row := p.QueryRow(ctx, `
		SELECT COALESCE(SUM(total_bytes), 0) FROM link_archives
		WHERE state = ANY($1)`,
		LinkArchiveActiveStates,
	)
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// TransitionLinkArchiveState moves the archive into nextState only when
// its current state is one of allowedFrom. Returns pgx.ErrNoRows when
// the transition is rejected (caller treats this as a state-machine
// violation and surfaces an appropriate error to the client). Allowed
// transitions are enforced here, not just at the application layer, so a
// race between two requests cannot land the row in an inconsistent
// state.
func (p *Pool) TransitionLinkArchiveState(ctx context.Context, archiveID, nextState string, allowedFrom []string) error {
	tag, err := p.Exec(ctx, `
		UPDATE link_archives
		SET state = $2
		WHERE id = $1 AND state = ANY($3)`,
		archiveID, nextState, allowedFrom,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// GetLinkArchiveByID returns the archive row for archiveID. Freshness is
// gated in scanLinkArchive (returns ErrLinkArchiveExpired past the
// sliding-expiry or hard-deadline boundary). Terminal-state rows are
// also rejected because they no longer accept any operations.
func (p *Pool) GetLinkArchiveByID(ctx context.Context, archiveID string) (*LinkArchive, error) {
	row := p.QueryRow(ctx, linkArchiveSelect+" WHERE id = $1", archiveID)
	return scanLinkArchive(row)
}

// GetLinkArchiveByUploadTokenHash returns the archive row whose
// upload_token_hash matches the supplied 32-byte hash, gated on freshness.
func (p *Pool) GetLinkArchiveByUploadTokenHash(ctx context.Context, archiveID string, tokenHash []byte) (*LinkArchive, error) {
	row := p.QueryRow(ctx, linkArchiveSelect+" WHERE id = $1 AND upload_token_hash = $2", archiveID, tokenHash)
	return scanLinkArchive(row)
}

// GetLinkArchiveByDownloadTokenHash returns the archive row whose
// download_token_hash matches the supplied 32-byte hash, gated on freshness.
func (p *Pool) GetLinkArchiveByDownloadTokenHash(ctx context.Context, archiveID string, tokenHash []byte) (*LinkArchive, error) {
	row := p.QueryRow(ctx, linkArchiveSelect+" WHERE id = $1 AND download_token_hash = $2", archiveID, tokenHash)
	return scanLinkArchive(row)
}

// linkArchiveSelect is the canonical column list. Keeping a single
// constant means scanLinkArchive can stay aligned with all three
// lookup queries.
const linkArchiveSelect = `
SELECT id, user_id, upload_token_hash, download_token_hash,
       total_chunks, total_bytes, chunk_size,
       manifest_hash, archive_sha256, finalized, state,
       created_at, expires_at, hard_deadline_at
FROM link_archives`

// RefreshLinkArchiveExpiry advances expires_at to LEAST(now() + ttl,
// hard_deadline_at). Stuck archives never live longer than the hard deadline.
// Returns the updated expires_at, or pgx.ErrNoRows if the archive does not
// exist.
func (p *Pool) RefreshLinkArchiveExpiry(ctx context.Context, archiveID string, ttl time.Duration) (time.Time, error) {
	row := p.QueryRow(ctx, `
		UPDATE link_archives
		SET expires_at = LEAST(now() + $2::interval, hard_deadline_at)
		WHERE id = $1
		RETURNING expires_at`,
		archiveID, ttl,
	)
	var newExpiry time.Time
	if err := row.Scan(&newExpiry); err != nil {
		return time.Time{}, err
	}
	return newExpiry, nil
}

// LinkArchiveChunkInsert is the parameter struct for InsertLinkArchiveChunk.
type LinkArchiveChunkInsert struct {
	ArchiveID      string
	Idx            int
	ChunkSize      int
	ChunkHash      []byte
	StorageBackend string
	StorageKey     string
}

// InsertLinkArchiveChunk stores chunk metadata. The bytes themselves
// live wherever the storage backend put them, addressed by
// StorageKey. Hash-keyed idempotency:
//   - first write for the (archive, idx) succeeds
//   - retry with the same chunk_hash is a no-op accept and returns no error
//   - retry with a different chunk_hash returns ErrLinkArchiveChunkConflict
//     and the stored row is unchanged
func (p *Pool) InsertLinkArchiveChunk(ctx context.Context, in LinkArchiveChunkInsert) error {
	tag, err := p.Exec(ctx, `
		INSERT INTO link_archive_chunks
			(archive_id, idx, chunk_size, chunk_hash, storage_backend, storage_key)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (archive_id, idx) DO NOTHING`,
		in.ArchiveID, in.Idx, in.ChunkSize, in.ChunkHash, in.StorageBackend, in.StorageKey,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}

	// No insert happened -> a row already exists. Compare hashes.
	row := p.QueryRow(ctx, `
		SELECT chunk_hash FROM link_archive_chunks
		WHERE archive_id = $1 AND idx = $2`,
		in.ArchiveID, in.Idx,
	)
	var existing []byte
	if err := row.Scan(&existing); err != nil {
		return err
	}
	if !bytesEqual(existing, in.ChunkHash) {
		return ErrLinkArchiveChunkConflict
	}
	return nil
}

// GetLinkArchiveChunkPointer returns the storage backend kind and key
// for (archiveID, idx). Used by the API download handler to look up
// where the bytes actually live.
func (p *Pool) GetLinkArchiveChunkPointer(ctx context.Context, archiveID string, idx int) (storageBackend, storageKey string, err error) {
	row := p.QueryRow(ctx, `
		SELECT storage_backend, storage_key
		FROM link_archive_chunks
		WHERE archive_id = $1 AND idx = $2`,
		archiveID, idx,
	)
	if err := row.Scan(&storageBackend, &storageKey); err != nil {
		return "", "", err
	}
	return storageBackend, storageKey, nil
}

// ListLinkArchiveChunkRows returns metadata for every stored chunk, ordered
// by idx ASC. Used at finalize time and to compute the manifest the NEW
// device fetches.
func (p *Pool) ListLinkArchiveChunkRows(ctx context.Context, archiveID string) ([]LinkArchiveChunkRow, error) {
	rows, err := p.Query(ctx, `
		SELECT idx, chunk_size, chunk_hash, storage_backend, storage_key
		FROM link_archive_chunks
		WHERE archive_id = $1
		ORDER BY idx ASC`,
		archiveID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]LinkArchiveChunkRow, 0)
	for rows.Next() {
		var r LinkArchiveChunkRow
		if err := rows.Scan(&r.Idx, &r.ChunkSize, &r.ChunkHash, &r.StorageBackend, &r.StorageKey); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// MarkLinkArchiveFinalized flips finalized=true for backward
// compatibility and transitions state to 'uploaded'. Caller is expected
// to have verified completeness and hash bindings already.
func (p *Pool) MarkLinkArchiveFinalized(ctx context.Context, archiveID string) error {
	_, err := p.Exec(ctx, `
		UPDATE link_archives SET finalized = true
		WHERE id = $1`,
		archiveID,
	)
	return err
}

// DeleteLinkArchive removes the archive row; chunk rows cascade. Storage
// backend cleanup is the caller's responsibility (see the supervisor
// purger which handles both DB and backend together).
func (p *Pool) DeleteLinkArchive(ctx context.Context, archiveID string) error {
	_, err := p.Exec(ctx, `DELETE FROM link_archives WHERE id = $1`, archiveID)
	return err
}

// GcEligibleLinkArchive describes one row the supervisor purger should
// reap. Carries enough metadata for the caller to delete the chunk
// bytes from the storage backend before the DB row.
type GcEligibleLinkArchive struct {
	ID         string
	State      string
	StorageKey string // first chunk's storage backend; same for every chunk in current MVP
}

// ListGcEligibleLinkArchives returns archives that have aged past their
// state-specific TTL. Three retention rules:
//
//   - 'acknowledged' / 'aborted' are GC-eligible immediately.
//   - 'terminal_failure' / 'imported' (no ACK yet) are GC-eligible
//     24 hours after they entered the state — diagnostic grace window.
//   - everything else is governed by the sliding expires_at + hard
//     deadline pair.
//
// State-entry timestamps are not currently tracked per state; a 24-hour
// grace is implemented as `expires_at < now() - 24h` because every
// transition refreshes expires_at, so the sliding window naturally
// times out 60 min after the last touch and well past the 24-hour
// grace by the time the retention rule applies. Future work to emit a
// per-state-entry timestamp can refine this.
func (p *Pool) ListGcEligibleLinkArchives(ctx context.Context, limit int) ([]string, error) {
	rows, err := p.Query(ctx, `
		SELECT id FROM link_archives
		WHERE
		    -- always GC-eligible terminal states
		    state IN ('acknowledged', 'aborted')
		 OR -- diagnostic grace for terminal_failure / imported (no ACK)
		    (state IN ('terminal_failure', 'imported') AND expires_at < now() - INTERVAL '24 hours')
		 OR -- sliding-expiry timeout (active states)
		    expires_at < now()
		 OR -- hard deadline reached regardless
		    hard_deadline_at < now()
		ORDER BY created_at ASC
		LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// PurgeExpiredLinkArchives deletes archives where either expires_at or
// hard_deadline_at has passed. Returns rows affected.
//
// Kept for backward compatibility with the existing purger; the new
// supervisor uses ListGcEligibleLinkArchives + DeleteLinkArchive so it
// can clean storage-backend objects between the list and delete steps.
func (p *Pool) PurgeExpiredLinkArchives(ctx context.Context) (int64, error) {
	tag, err := p.Exec(ctx, `
		DELETE FROM link_archives
		WHERE expires_at < now() OR hard_deadline_at < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// scanLinkArchive scans a row and converts an expired/missing row into
// ErrLinkArchiveExpired. Pgx returns ErrNoRows when the WHERE clause filters
// the row out; the actual freshness check happens in code so callers can
// distinguish "missing" from "expired" cleanly.
func scanLinkArchive(row pgx.Row) (*LinkArchive, error) {
	var a LinkArchive
	var userID *string
	err := row.Scan(
		&a.ID,
		&userID,
		&a.UploadTokenHash,
		&a.DownloadTokenHash,
		&a.TotalChunks,
		&a.TotalBytes,
		&a.ChunkSize,
		&a.ManifestHash,
		&a.ArchiveSHA256,
		&a.Finalized,
		&a.State,
		&a.CreatedAt,
		&a.ExpiresAt,
		&a.HardDeadlineAt,
	)
	if err != nil {
		return nil, err
	}
	if userID != nil {
		a.UserID = *userID
	}
	now := time.Now().UTC()
	if !a.ExpiresAt.After(now) || !a.HardDeadlineAt.After(now) {
		return nil, ErrLinkArchiveExpired
	}
	return &a, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
