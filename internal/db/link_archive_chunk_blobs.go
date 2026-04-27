package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// UpsertChunkBlob writes (storage_key, bytes) to link_archive_chunk_blobs.
// The same storage_key may legitimately appear twice when a client
// retries an upload with identical bytes; ON CONFLICT DO NOTHING keeps
// the first writer and lets the API layer detect hash mismatches at
// the chunk-row level.
func (p *Pool) UpsertChunkBlob(ctx context.Context, storageKey string, bytes []byte) error {
	_, err := p.Exec(ctx, `
		INSERT INTO link_archive_chunk_blobs (storage_key, bytes)
		VALUES ($1, $2)
		ON CONFLICT (storage_key) DO NOTHING`,
		storageKey, bytes,
	)
	return err
}

// GetChunkBlob fetches the bytes previously stored under storageKey.
// Returns pgx.ErrNoRows when absent.
func (p *Pool) GetChunkBlob(ctx context.Context, storageKey string) ([]byte, error) {
	row := p.QueryRow(ctx, `
		SELECT bytes FROM link_archive_chunk_blobs WHERE storage_key = $1`,
		storageKey,
	)
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// DeleteChunkBlob removes the row. Missing rows are treated as success.
func (p *Pool) DeleteChunkBlob(ctx context.Context, storageKey string) error {
	_, err := p.Exec(ctx, `
		DELETE FROM link_archive_chunk_blobs WHERE storage_key = $1`,
		storageKey,
	)
	return err
}

// ChunkBlobExists is a cheap existence probe.
func (p *Pool) ChunkBlobExists(ctx context.Context, storageKey string) (bool, error) {
	row := p.QueryRow(ctx, `
		SELECT 1 FROM link_archive_chunk_blobs WHERE storage_key = $1`,
		storageKey,
	)
	var one int
	if err := row.Scan(&one); err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
