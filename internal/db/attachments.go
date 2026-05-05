package db

import (
	"context"
	"errors"
	"time"

	"github.com/hushhq/hush-server/internal/models"

	"github.com/jackc/pgx/v5"
)

// InsertAttachment records a new attachment row before the client
// uploads bytes to the storage backend. Callers must Soft-delete the
// row if the upload never lands so the supervised purger can drop the
// orphan after the presign TTL expires.
func (p *Pool) InsertAttachment(ctx context.Context, channelID, ownerID, storageKey, contentType string, size int64) (*models.Attachment, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO attachments (channel_id, owner_id, storage_key, size, content_type)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, channel_id, owner_id, storage_key, size, content_type, created_at, deleted_at`,
		channelID, ownerID, storageKey, size, contentType,
	)
	return scanAttachment(row)
}

// GetAttachmentByID fetches an attachment that has not been soft-deleted.
// Returns pgx.ErrNoRows when the row does not exist or is already
// tombstoned — callers map both to 404.
func (p *Pool) GetAttachmentByID(ctx context.Context, attachmentID string) (*models.Attachment, error) {
	row := p.QueryRow(ctx, `
		SELECT id, channel_id, owner_id, storage_key, size, content_type, created_at, deleted_at
		FROM attachments
		WHERE id = $1 AND deleted_at IS NULL`,
		attachmentID,
	)
	return scanAttachment(row)
}

// SoftDeleteAttachment marks the row as deleted and returns the freshly
// loaded row so the caller can hand the storage_key to the configured
// Backend.Delete. Returns pgx.ErrNoRows if the attachment does not
// exist, is already deleted, or does not belong to ownerID.
func (p *Pool) SoftDeleteAttachment(ctx context.Context, attachmentID, ownerID string) (*models.Attachment, error) {
	row := p.QueryRow(ctx, `
		UPDATE attachments
		SET deleted_at = now()
		WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL
		RETURNING id, channel_id, owner_id, storage_key, size, content_type, created_at, deleted_at`,
		attachmentID, ownerID,
	)
	return scanAttachment(row)
}

func scanAttachment(row pgx.Row) (*models.Attachment, error) {
	var (
		a         models.Attachment
		deletedAt *time.Time
	)
	if err := row.Scan(&a.ID, &a.ChannelID, &a.OwnerID, &a.StorageKey, &a.Size, &a.ContentType, &a.CreatedAt, &deletedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, pgx.ErrNoRows
		}
		return nil, err
	}
	a.DeletedAt = deletedAt
	return &a, nil
}
