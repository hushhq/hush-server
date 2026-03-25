package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"hush.app/server/internal/models"
)

// InsertTransparencyLogEntry persists a fully-signed transparency log entry.
// Called after Append + Countersign within TransparencyService.AppendEntry.
func (p *Pool) InsertTransparencyLogEntry(
	ctx context.Context,
	leafIndex uint64,
	operation string,
	userPubKey, subjectKey, entryCBOR, leafHash, userSig, logSig []byte,
) error {
	const q = `
		INSERT INTO transparency_log_entries
		    (leaf_index, operation, user_pub_key, subject_key, entry_cbor, leaf_hash, user_sig, log_sig)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`

	_, err := p.Exec(ctx, q,
		int64(leafIndex), operation, userPubKey, subjectKey,
		entryCBOR, leafHash, userSig, logSig,
	)
	if err != nil {
		return fmt.Errorf("db: insert transparency log entry: %w", err)
	}
	return nil
}

// GetTransparencyLogEntriesByPubKey returns all log entries for a public key,
// ordered by leaf_index ASC.
func (p *Pool) GetTransparencyLogEntriesByPubKey(
	ctx context.Context,
	pubKey []byte,
) ([]models.TransparencyLogEntry, error) {
	const q = `
		SELECT id, leaf_index, operation, user_pub_key, subject_key,
		       entry_cbor, leaf_hash, user_sig, log_sig, logged_at
		  FROM transparency_log_entries
		 WHERE user_pub_key = $1
		 ORDER BY leaf_index ASC`

	rows, err := p.Query(ctx, q, pubKey)
	if err != nil {
		return nil, fmt.Errorf("db: get transparency entries by pubkey: %w", err)
	}
	defer rows.Close()

	var entries []models.TransparencyLogEntry
	for rows.Next() {
		var e models.TransparencyLogEntry
		var leafIdx int64
		if err := rows.Scan(
			&e.ID, &leafIdx, &e.Operation, &e.UserPubKey, &e.SubjectKey,
			&e.EntryCBOR, &e.LeafHash, &e.UserSig, &e.LogSig, &e.LoggedAt,
		); err != nil {
			return nil, fmt.Errorf("db: scan transparency entry: %w", err)
		}
		e.LeafIndex = uint64(leafIdx)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("db: iterate transparency entries: %w", err)
	}
	return entries, nil
}

// GetLatestTransparencyTreeHead returns the tree head with the highest tree_size.
// Returns nil (no error) when the table is empty.
func (p *Pool) GetLatestTransparencyTreeHead(
	ctx context.Context,
) (*models.TransparencyTreeHead, error) {
	const q = `
		SELECT tree_size, root_hash, fringe, head_sig, created_at
		  FROM transparency_tree_heads
		 ORDER BY tree_size DESC
		 LIMIT 1`

	var head models.TransparencyTreeHead
	var treeSize int64
	err := p.QueryRow(ctx, q).Scan(
		&treeSize, &head.RootHash, &head.Fringe, &head.HeadSig, &head.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("db: get latest tree head: %w", err)
	}
	head.TreeSize = uint64(treeSize)
	return &head, nil
}

// InsertTransparencyTreeHead persists the Merkle tree state after a successful append.
// The primary key is tree_size, so each unique tree size is stored exactly once.
func (p *Pool) InsertTransparencyTreeHead(
	ctx context.Context,
	treeSize uint64,
	rootHash, fringe, headSig []byte,
) error {
	const q = `
		INSERT INTO transparency_tree_heads (tree_size, root_hash, fringe, head_sig)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tree_size) DO UPDATE
		    SET root_hash = EXCLUDED.root_hash,
		        fringe    = EXCLUDED.fringe,
		        head_sig  = EXCLUDED.head_sig`

	_, err := p.Exec(ctx, q, int64(treeSize), rootHash, fringe, headSig)
	if err != nil {
		return fmt.Errorf("db: insert transparency tree head: %w", err)
	}
	return nil
}
