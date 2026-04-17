package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// GetUnreadCount returns the count of messages in channelID that are visible to
// userID (recipient_id IS NULL OR recipient_id = userID), not sent by userID,
// and newer than the user's read marker. Returns 0 if no marker exists and
// there are no messages.
func (p *Pool) GetUnreadCount(ctx context.Context, channelID, userID string) (int, error) {
	var count int
	err := p.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM messages
		WHERE channel_id = $1::uuid
		  AND (recipient_id IS NULL OR recipient_id = $2::uuid)
		  AND (sender_id IS NULL OR sender_id != $2::uuid)
		  AND "timestamp" > COALESCE(
		      (SELECT read_up_to_ts FROM read_markers
		       WHERE channel_id = $1::uuid AND user_id = $2::uuid LIMIT 1),
		      '-infinity'::timestamptz
		  )`,
		channelID, userID,
	).Scan(&count)
	return count, err
}

// MarkChannelRead advances the read marker for (channelID, userID) to the stored
// timestamp of messageID. The marker never moves backward. Returns an error if
// the message does not exist, does not belong to channelID, or is not visible to userID.
func (p *Pool) MarkChannelRead(ctx context.Context, channelID, userID, messageID string) error {
	var ts time.Time

	err := p.QueryRow(ctx, `
		SELECT "timestamp"
		FROM messages
		WHERE id = $1::uuid
		  AND channel_id = $2::uuid
		  AND (recipient_id IS NULL OR recipient_id = $3::uuid)`,
		messageID, channelID, userID,
	).Scan(&ts)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("message not found or not visible")
	}
	if err != nil {
		return err
	}

	_, err = p.Exec(ctx, `
		INSERT INTO read_markers (channel_id, user_id, read_up_to_msg_id, read_up_to_ts, updated_at)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4, now())
		ON CONFLICT (channel_id, user_id)
		DO UPDATE SET
		    read_up_to_msg_id = EXCLUDED.read_up_to_msg_id,
		    read_up_to_ts     = EXCLUDED.read_up_to_ts,
		    updated_at        = now()
		WHERE read_markers.read_up_to_ts IS NULL
		   OR read_markers.read_up_to_ts < EXCLUDED.read_up_to_ts`,
		channelID, userID, messageID, ts,
	)
	return err
}
