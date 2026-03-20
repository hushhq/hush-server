package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// MLSCommitRow represents one row from the mls_commits table.
type MLSCommitRow struct {
	Epoch       int64
	CommitBytes []byte
	SenderID    string
	CreatedAt   time.Time
}

// PendingWelcomeRow represents one row from the mls_pending_welcomes table.
type PendingWelcomeRow struct {
	ID           string
	ChannelID    string
	WelcomeBytes []byte
	SenderID     string
	Epoch        int64
	CreatedAt    time.Time
}

// UpsertMLSGroupInfo inserts or updates the GroupInfo bytes for a channel.
// On conflict (channel_id) the group_info_bytes, epoch, and updated_at are refreshed.
func (p *Pool) UpsertMLSGroupInfo(ctx context.Context, channelID string, groupInfoBytes []byte, epoch int64) error {
	_, err := p.Exec(ctx, `
		INSERT INTO mls_group_info (channel_id, group_info_bytes, epoch, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (channel_id) DO UPDATE SET
			group_info_bytes = EXCLUDED.group_info_bytes,
			epoch            = EXCLUDED.epoch,
			updated_at       = now()`,
		channelID, groupInfoBytes, epoch,
	)
	return err
}

// GetMLSGroupInfo returns the stored GroupInfo bytes and epoch for a channel.
// Returns (nil, 0, nil) when no row exists.
func (p *Pool) GetMLSGroupInfo(ctx context.Context, channelID string) (groupInfoBytes []byte, epoch int64, err error) {
	err = p.QueryRow(ctx, `
		SELECT group_info_bytes, epoch
		FROM mls_group_info
		WHERE channel_id = $1`,
		channelID,
	).Scan(&groupInfoBytes, &epoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	return groupInfoBytes, epoch, err
}

// AppendMLSCommit inserts a Commit into the commit queue for a channel.
func (p *Pool) AppendMLSCommit(ctx context.Context, channelID string, epoch int64, commitBytes []byte, senderID string) error {
	_, err := p.Exec(ctx, `
		INSERT INTO mls_commits (channel_id, epoch, commit_bytes, sender_id)
		VALUES ($1, $2, $3, $4)`,
		channelID, epoch, commitBytes, senderID,
	)
	return err
}

// GetMLSCommitsSinceEpoch returns commits for a channel with epoch > sinceEpoch,
// ordered by epoch ascending, limited to at most limit rows.
func (p *Pool) GetMLSCommitsSinceEpoch(ctx context.Context, channelID string, sinceEpoch int64, limit int) ([]MLSCommitRow, error) {
	rows, err := p.Query(ctx, `
		SELECT epoch, commit_bytes, sender_id, created_at
		FROM mls_commits
		WHERE channel_id = $1 AND epoch > $2
		ORDER BY epoch ASC
		LIMIT $3`,
		channelID, sinceEpoch, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var commits []MLSCommitRow
	for rows.Next() {
		var row MLSCommitRow
		if err := rows.Scan(&row.Epoch, &row.CommitBytes, &row.SenderID, &row.CreatedAt); err != nil {
			return nil, err
		}
		commits = append(commits, row)
	}
	return commits, rows.Err()
}

// DeleteMLSGroupInfo removes the GroupInfo row for a channel.
// Associated commits in mls_commits are deleted by the channel FK cascade on channels.
func (p *Pool) DeleteMLSGroupInfo(ctx context.Context, channelID string) error {
	_, err := p.Exec(ctx, `
		DELETE FROM mls_group_info WHERE channel_id = $1`,
		channelID,
	)
	return err
}

// PurgeOldMLSCommits deletes commits beyond maxPerChannel per channel, keeping
// the most recent maxPerChannel commits. Processes at most 10 000 excess rows per
// call to bound the DELETE scan time. Returns the total number of rows deleted.
func (p *Pool) PurgeOldMLSCommits(ctx context.Context, maxPerChannel int) (int64, error) {
	tag, err := p.Exec(ctx, `
		DELETE FROM mls_commits
		WHERE id IN (
			SELECT id FROM mls_commits AS c
			WHERE (
				SELECT COUNT(*) FROM mls_commits
				WHERE channel_id = c.channel_id AND epoch >= c.epoch
			) > $1
			ORDER BY epoch ASC
			LIMIT 10000
		)`,
		maxPerChannel,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// StorePendingWelcome stores a Welcome message to be delivered to an offline member.
func (p *Pool) StorePendingWelcome(ctx context.Context, channelID, recipientUserID, senderID string, welcomeBytes []byte, epoch int64) error {
	_, err := p.Exec(ctx, `
		INSERT INTO mls_pending_welcomes (channel_id, recipient_user_id, sender_id, welcome_bytes, epoch)
		VALUES ($1, $2, $3, $4, $5)`,
		channelID, recipientUserID, senderID, welcomeBytes, epoch,
	)
	return err
}

// GetPendingWelcomes returns all pending Welcome messages for a recipient user,
// ordered by created_at ascending (oldest first).
func (p *Pool) GetPendingWelcomes(ctx context.Context, recipientUserID string) ([]PendingWelcomeRow, error) {
	rows, err := p.Query(ctx, `
		SELECT id, channel_id, welcome_bytes, sender_id, epoch, created_at
		FROM mls_pending_welcomes
		WHERE recipient_user_id = $1
		ORDER BY created_at ASC`,
		recipientUserID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var welcomes []PendingWelcomeRow
	for rows.Next() {
		var row PendingWelcomeRow
		if err := rows.Scan(&row.ID, &row.ChannelID, &row.WelcomeBytes, &row.SenderID, &row.Epoch, &row.CreatedAt); err != nil {
			return nil, err
		}
		welcomes = append(welcomes, row)
	}
	return welcomes, rows.Err()
}

// DeletePendingWelcome removes a specific pending Welcome after the client ACKs it.
func (p *Pool) DeletePendingWelcome(ctx context.Context, welcomeID string) error {
	_, err := p.Exec(ctx, `
		DELETE FROM mls_pending_welcomes WHERE id = $1`,
		welcomeID,
	)
	return err
}
