package db

import (
	"context"
	"time"

	"hush.app/server/internal/models"

	"github.com/jackc/pgx/v5"
)

// InsertMessage stores an encrypted message and returns the created row.
func (p *Pool) InsertMessage(ctx context.Context, channelID, senderID string, ciphertext []byte) (*models.Message, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO messages (channel_id, sender_id, ciphertext)
		VALUES ($1, $2, $3)
		RETURNING id, channel_id, sender_id, ciphertext, "timestamp"`,
		channelID, senderID, ciphertext,
	)
	return scanMessage(row)
}

// GetMessages returns messages for the channel ordered by timestamp DESC (newest first).
// before is the cursor; use zero value for initial page. limit is capped by caller.
func (p *Pool) GetMessages(ctx context.Context, channelID string, before time.Time, limit int) ([]models.Message, error) {
	if limit <= 0 {
		return nil, nil
	}
	if before.IsZero() {
		rows, err := p.Query(ctx, `
			SELECT id, channel_id, sender_id, ciphertext, "timestamp"
			FROM messages
			WHERE channel_id = $1
			ORDER BY "timestamp" DESC
			LIMIT $2`, channelID, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanMessages(rows)
	}
	rows, err := p.Query(ctx, `
		SELECT id, channel_id, sender_id, ciphertext, "timestamp"
		FROM messages
		WHERE channel_id = $1 AND "timestamp" < $2
		ORDER BY "timestamp" DESC
		LIMIT $3`, channelID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

// IsChannelMember returns true if userID is a member of the server that owns the channel.
func (p *Pool) IsChannelMember(ctx context.Context, channelID, userID string) (bool, error) {
	var exists bool
	err := p.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM channels c
			INNER JOIN server_members sm ON sm.server_id = c.server_id AND sm.user_id = $2
			WHERE c.id = $1
		)`, channelID, userID).Scan(&exists)
	return exists, err
}

func scanMessage(row pgx.Row) (*models.Message, error) {
	var m models.Message
	err := row.Scan(&m.ID, &m.ChannelID, &m.SenderID, &m.Ciphertext, &m.Timestamp)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func scanMessages(rows pgx.Rows) ([]models.Message, error) {
	defer rows.Close()
	var out []models.Message
	for rows.Next() {
		var m models.Message
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.SenderID, &m.Ciphertext, &m.Timestamp); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
