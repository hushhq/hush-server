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

// GetAttachmentByID fetches an attachment row, including soft-deleted rows.
// Callers check DeletedAt to return a stable "gone" placeholder for messages
// that still reference a quota- or retention-purged attachment.
func (p *Pool) GetAttachmentByID(ctx context.Context, attachmentID string) (*models.Attachment, error) {
	row := p.QueryRow(ctx, `
		SELECT id, channel_id, owner_id, storage_key, size, content_type, created_at, deleted_at
		FROM attachments
		WHERE id = $1`,
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

// SoftDeleteAttachmentsByID tombstones active attachments and returns the
// rows that changed state so callers can delete the corresponding storage
// objects. Missing or already-deleted ids are ignored.
func (p *Pool) SoftDeleteAttachmentsByID(ctx context.Context, attachmentIDs []string) ([]models.Attachment, error) {
	if len(attachmentIDs) == 0 {
		return nil, nil
	}
	rows, err := p.Query(ctx, `
		UPDATE attachments
		SET deleted_at = now()
		WHERE id = ANY($1::uuid[]) AND deleted_at IS NULL
		RETURNING id, channel_id, owner_id, storage_key, size, content_type, created_at, deleted_at`,
		attachmentIDs,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAttachmentRows(rows)
}

// ListAttachmentsForGuildQuota returns the channel's guild id and all active
// attachments in that guild, oldest first. The presign path uses this to
// decide which objects to tombstone when a new attachment would exceed the
// configured guild storage quota.
func (p *Pool) ListAttachmentsForGuildQuota(ctx context.Context, channelID string) (string, []models.Attachment, error) {
	var serverID string
	if err := p.QueryRow(ctx, `
		SELECT server_id FROM channels WHERE id = $1`,
		channelID,
	).Scan(&serverID); err != nil {
		return "", nil, err
	}
	rows, err := p.Query(ctx, `
		SELECT a.id, a.channel_id, a.owner_id, a.storage_key, a.size, a.content_type, a.created_at, a.deleted_at
		FROM attachments a
		JOIN channels c ON c.id = a.channel_id
		WHERE c.server_id = $1 AND a.deleted_at IS NULL
		ORDER BY a.created_at ASC, a.id ASC`,
		serverID,
	)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	active, err := scanAttachmentRows(rows)
	return serverID, active, err
}

// ListExpiredAttachments returns active attachment rows older than the message
// retention window. The caller deletes storage objects after tombstoning them.
func (p *Pool) ListExpiredAttachments(ctx context.Context, retentionDays int, limit int) ([]models.Attachment, error) {
	if retentionDays <= 0 || limit <= 0 {
		return nil, nil
	}
	rows, err := p.Query(ctx, `
		SELECT id, channel_id, owner_id, storage_key, size, content_type, created_at, deleted_at
		FROM attachments
		WHERE deleted_at IS NULL
		  AND created_at < now() - ($1 * interval '1 day')
		ORDER BY created_at ASC, id ASC
		LIMIT $2`,
		retentionDays, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAttachmentRows(rows)
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

func scanAttachmentRows(rows pgx.Rows) ([]models.Attachment, error) {
	var out []models.Attachment
	for rows.Next() {
		var (
			a         models.Attachment
			deletedAt *time.Time
		)
		if err := rows.Scan(&a.ID, &a.ChannelID, &a.OwnerID, &a.StorageKey, &a.Size, &a.ContentType, &a.CreatedAt, &deletedAt); err != nil {
			return nil, err
		}
		a.DeletedAt = deletedAt
		out = append(out, a)
	}
	return out, rows.Err()
}
