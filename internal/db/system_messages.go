package db

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hushhq/hush-server/internal/models"
)

// InsertSystemMessage inserts a system message row and returns it with generated UUID and timestamp.
func (p *Pool) InsertSystemMessage(ctx context.Context, serverID, eventType, actorID string, targetID *string, reason string, metadata map[string]interface{}) (*models.SystemMessage, error) {
	var metadataJSON []byte
	if metadata != nil {
		var err error
		metadataJSON, err = json.Marshal(metadata)
		if err != nil {
			return nil, fmt.Errorf("marshal metadata: %w", err)
		}
	}

	row := p.QueryRow(ctx, `
		INSERT INTO system_messages (server_id, event_type, actor_id, target_id, reason, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, server_id, event_type, actor_id, target_id, reason, metadata, created_at`,
		serverID, eventType, actorID, targetID, reason, metadataJSON,
	)

	var msg models.SystemMessage
	var rawMeta []byte
	if err := row.Scan(&msg.ID, &msg.ServerID, &msg.EventType, &msg.ActorID, &msg.TargetID, &msg.Reason, &rawMeta, &msg.CreatedAt); err != nil {
		return nil, err
	}
	if rawMeta != nil {
		_ = json.Unmarshal(rawMeta, &msg.Metadata)
	}
	return &msg, nil
}

// ListSystemMessages returns system messages for a guild, ordered by created_at DESC.
// If before is zero time, no cursor filter is applied.
func (p *Pool) ListSystemMessages(ctx context.Context, serverID string, before time.Time, limit int) ([]models.SystemMessage, error) {
	var query string
	var args []interface{}

	if before.IsZero() {
		query = `
			SELECT id, server_id, event_type, actor_id, target_id, reason, metadata, created_at
			FROM system_messages
			WHERE server_id = $1
			ORDER BY created_at DESC
			LIMIT $2`
		args = []interface{}{serverID, limit}
	} else {
		query = `
			SELECT id, server_id, event_type, actor_id, target_id, reason, metadata, created_at
			FROM system_messages
			WHERE server_id = $1 AND created_at < $2
			ORDER BY created_at DESC
			LIMIT $3`
		args = []interface{}{serverID, before, limit}
	}

	rows, err := p.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []models.SystemMessage
	for rows.Next() {
		var msg models.SystemMessage
		var rawMeta []byte
		if err := rows.Scan(&msg.ID, &msg.ServerID, &msg.EventType, &msg.ActorID, &msg.TargetID, &msg.Reason, &rawMeta, &msg.CreatedAt); err != nil {
			return nil, err
		}
		if rawMeta != nil {
			_ = json.Unmarshal(rawMeta, &msg.Metadata)
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

// PurgeExpiredSystemMessages deletes system messages older than retentionDays.
// Returns the number of rows deleted.
func (p *Pool) PurgeExpiredSystemMessages(ctx context.Context, retentionDays int) (int64, error) {
	tag, err := p.Exec(ctx,
		`DELETE FROM system_messages WHERE created_at < now() - ($1 * interval '1 day')`,
		retentionDays)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// GetSystemMessageRetentionDays returns the configured retention period from instance_config.
// Returns nil if the value is NULL (meaning keep forever).
func (p *Pool) GetSystemMessageRetentionDays(ctx context.Context) (*int, error) {
	row := p.QueryRow(ctx, `SELECT system_message_retention_days FROM instance_config LIMIT 1`)
	var days *int
	if err := row.Scan(&days); err != nil {
		return nil, err
	}
	return days, nil
}
