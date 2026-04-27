package db

import (
	"context"
	"errors"
	"time"

	"github.com/hushhq/hush-server/internal/models"

	"github.com/jackc/pgx/v5"
)

// InsertMessage stores an encrypted message and returns the created row.
// Exactly one of senderID or federatedSenderID should be non-nil.
// recipientID nil = broadcast/single ciphertext; non-nil = fan-out for that recipient.
func (p *Pool) InsertMessage(ctx context.Context, channelID string, senderID *string, federatedSenderID *string, recipientID *string, ciphertext []byte) (*models.Message, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO messages (channel_id, sender_id, federated_sender_id, recipient_id, ciphertext)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, channel_id, sender_id, federated_sender_id, recipient_id, ciphertext, "timestamp"`,
		channelID, senderID, federatedSenderID, recipientID, ciphertext,
	)
	return scanMessage(row)
}

// GetMessages returns messages for the channel for the given recipient (rows where recipient_id IS NULL OR recipient_id = recipientID), ordered by timestamp DESC.
func (p *Pool) GetMessages(ctx context.Context, channelID, recipientID string, before time.Time, limit int) ([]models.Message, error) {
	if limit <= 0 {
		return nil, nil
	}
	if before.IsZero() {
		rows, err := p.Query(ctx, `
			SELECT id, channel_id, sender_id, federated_sender_id, recipient_id, ciphertext, "timestamp"
			FROM messages
			WHERE channel_id = $1 AND (recipient_id IS NULL OR recipient_id = $2)
			ORDER BY "timestamp" DESC
			LIMIT $3`, channelID, recipientID, limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanMessages(rows)
	}
	rows, err := p.Query(ctx, `
		SELECT id, channel_id, sender_id, federated_sender_id, recipient_id, ciphertext, "timestamp"
		FROM messages
		WHERE channel_id = $1 AND (recipient_id IS NULL OR recipient_id = $2) AND "timestamp" < $3
		ORDER BY "timestamp" DESC
		LIMIT $4`, channelID, recipientID, before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

// GetMessagesAfter returns messages for the channel newer than the given timestamp, ordered
// by timestamp ASC. Used for reconnect catch-up: the client sends the timestamp of the last
// known message and receives any messages created after that point in chronological order.
func (p *Pool) GetMessagesAfter(ctx context.Context, channelID, recipientID string, after time.Time, limit int) ([]models.Message, error) {
	if limit <= 0 {
		return nil, nil
	}
	rows, err := p.Query(ctx, `
		SELECT id, channel_id, sender_id, federated_sender_id, recipient_id, ciphertext, "timestamp"
		FROM messages
		WHERE channel_id = $1 AND (recipient_id IS NULL OR recipient_id = $2) AND "timestamp" > $3
		ORDER BY "timestamp" ASC
		LIMIT $4`, channelID, recipientID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

// GetMessageByID returns the message with the given ID, or nil if not found.
func (p *Pool) GetMessageByID(ctx context.Context, messageID string) (*models.Message, error) {
	row := p.QueryRow(ctx, `
		SELECT id, channel_id, sender_id, federated_sender_id, recipient_id, ciphertext, "timestamp"
		FROM messages
		WHERE id = $1`,
		messageID,
	)
	msg, err := scanMessage(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return msg, err
}

// DeleteMessage removes the message iff it lives in a channel whose
// server_id matches serverID. The server filter is enforced via a
// subquery so cross-guild attempts cannot delete the row even when the
// caller knows the messageID. Returns pgx.ErrNoRows on cross-guild
// mismatch so handlers can map the result to 404.
func (p *Pool) DeleteMessage(ctx context.Context, messageID, serverID string) error {
	tag, err := p.Exec(ctx, `
		DELETE FROM messages
		WHERE id = $1
		  AND channel_id IN (SELECT id FROM channels WHERE server_id = $2)`,
		messageID, serverID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func scanMessage(row pgx.Row) (*models.Message, error) {
	var m models.Message
	err := row.Scan(&m.ID, &m.ChannelID, &m.SenderID, &m.FederatedSenderID, &m.RecipientID, &m.Ciphertext, &m.Timestamp)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func scanMessages(rows pgx.Rows) ([]models.Message, error) {
	var out []models.Message
	for rows.Next() {
		var m models.Message
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.SenderID, &m.FederatedSenderID, &m.RecipientID, &m.Ciphertext, &m.Timestamp); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
