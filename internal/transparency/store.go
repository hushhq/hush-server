package transparency

import (
	"context"

	"hush.app/server/internal/models"
)

// TransparencyStore is the persistence contract for the transparency log.
// The pgx implementation in db/transparency.go satisfies this interface.
// A mock can be injected in unit tests.
type TransparencyStore interface {
	// InsertLogEntry persists one log entry after it has been appended to the
	// in-memory Merkle tree and countersigned by the log. All fields are
	// mandatory (nil slices are rejected at the DB level).
	InsertLogEntry(ctx context.Context, leafIndex uint64, entry *LogEntry, cborBytes, leafHash, logSig []byte) error

	// GetLogEntriesByPubKey returns all log entries for the given user public key,
	// ordered by leaf_index ASC. Returns an empty slice (not an error) when no
	// entries exist.
	GetLogEntriesByPubKey(ctx context.Context, pubKey []byte) ([]models.TransparencyLogEntry, error)

	// GetAllLeafHashes returns every leaf_hash in leaf_index order.
	// Used at startup to rehydrate the Merkle tree's leaves slice so
	// Proof() can rebuild the full tree for any leaf index.
	GetAllLeafHashes(ctx context.Context) ([][32]byte, error)

	// GetLatestTreeHead returns the most recently persisted tree head, used to
	// restore the Merkle tree fringe after a server restart. Returns nil (no
	// error) when the log is empty.
	GetLatestTreeHead(ctx context.Context) (*models.TransparencyTreeHead, error)

	// InsertTreeHead persists the Merkle tree state after each append. The fringe
	// is the serialized right-edge sibling hashes (fringeToBytes format).
	InsertTreeHead(ctx context.Context, treeSize uint64, rootHash, fringe, headSig []byte) error
}
