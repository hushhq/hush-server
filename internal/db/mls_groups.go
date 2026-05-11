package db

import (
	"context"
	"errors"
	"time"

	"github.com/hushhq/hush-server/internal/version"
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

// UpsertMLSGroupInfo inserts or updates the GroupInfo bytes for a channel and group type
// under the active server ciphersuite. groupType must be "text" or "voice". A legacy
// suite row for the same (channel, group_type) is left intact; the partial unique
// index on (channel_id, group_type, ciphersuite) keeps the two rows from colliding.
// On conflict at the current suite the group_info_bytes, epoch, and updated_at are
// refreshed.
func (p *Pool) UpsertMLSGroupInfo(ctx context.Context, channelID string, groupType string, groupInfoBytes []byte, epoch int64) error {
	_, err := p.Exec(ctx, `
		INSERT INTO mls_group_info (channel_id, group_type, group_info_bytes, epoch, ciphersuite, updated_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (channel_id, group_type, ciphersuite) WHERE channel_id IS NOT NULL DO UPDATE SET
			group_info_bytes = EXCLUDED.group_info_bytes,
			epoch            = EXCLUDED.epoch,
			updated_at       = now()`,
		channelID, groupType, groupInfoBytes, epoch, version.CurrentMLSCiphersuite,
	)
	return err
}

// GetMLSGroupInfo returns the GroupInfo bytes and epoch for a channel and group type
// at the current server ciphersuite. groupType must be "text" or "voice". Legacy-suite
// rows are never returned: a client running the current protocol must be able to treat
// a missing row as "no group at the active suite, start a fresh one". Returns
// (nil, 0, nil) when no current-suite row exists.
func (p *Pool) GetMLSGroupInfo(ctx context.Context, channelID string, groupType string) (groupInfoBytes []byte, epoch int64, err error) {
	err = p.QueryRow(ctx, `
		SELECT group_info_bytes, epoch
		FROM mls_group_info
		WHERE channel_id  = $1
		  AND group_type  = $2
		  AND ciphersuite = $3`,
		channelID, groupType, version.CurrentMLSCiphersuite,
	).Scan(&groupInfoBytes, &epoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	return groupInfoBytes, epoch, err
}

// AppendMLSCommit inserts a Commit into the commit queue for a channel under the
// active server ciphersuite.
func (p *Pool) AppendMLSCommit(ctx context.Context, channelID string, epoch int64, commitBytes []byte, senderID string) error {
	_, err := p.Exec(ctx, `
		INSERT INTO mls_commits (channel_id, epoch, commit_bytes, sender_id, ciphersuite)
		VALUES ($1, $2, $3, $4, $5)`,
		channelID, epoch, commitBytes, senderID, version.CurrentMLSCiphersuite,
	)
	return err
}

// GetMLSCommitsSinceEpoch returns commits for a channel at the current server
// ciphersuite with epoch > sinceEpoch, ordered by epoch ascending, limited to at
// most limit rows. Legacy-suite commits are filtered out: replaying them under the
// new protocol epoch would either fail validation or corrupt local state, so they
// must never reach a current-suite client.
func (p *Pool) GetMLSCommitsSinceEpoch(ctx context.Context, channelID string, sinceEpoch int64, limit int) ([]MLSCommitRow, error) {
	rows, err := p.Query(ctx, `
		WITH ordered_commits AS (
			SELECT
				CASE
					WHEN epoch > 0 THEN epoch
					ELSE ROW_NUMBER() OVER (PARTITION BY channel_id ORDER BY created_at ASC, id ASC)
				END AS effective_epoch,
				commit_bytes,
				sender_id,
				created_at
			FROM mls_commits
			WHERE channel_id  = $1
			  AND ciphersuite = $4
		)
		SELECT effective_epoch, commit_bytes, sender_id, created_at
		FROM ordered_commits
		WHERE effective_epoch > $2
		ORDER BY effective_epoch ASC
		LIMIT $3`,
		channelID, sinceEpoch, limit, version.CurrentMLSCiphersuite,
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

// DeleteMLSGroupInfo removes the GroupInfo row for a channel and group type at
// the current server ciphersuite. groupType must be "text" or "voice". For voice
// groups this is called when the last participant leaves the voice channel to
// enforce the clean forward-secrecy boundary between voice sessions. Legacy-suite
// rows are intentionally preserved: they are invisible to consumers and serve as
// audit history that the operator can purge in a separate intentional action.
func (p *Pool) DeleteMLSGroupInfo(ctx context.Context, channelID string, groupType string) error {
	_, err := p.Exec(ctx, `
		DELETE FROM mls_group_info
		WHERE channel_id  = $1
		  AND group_type  = $2
		  AND ciphersuite = $3`,
		channelID, groupType, version.CurrentMLSCiphersuite,
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

// StorePendingWelcome stores a Welcome message to be delivered to an offline member
// under the active server ciphersuite.
func (p *Pool) StorePendingWelcome(ctx context.Context, channelID, recipientUserID, senderID string, welcomeBytes []byte, epoch int64) error {
	_, err := p.Exec(ctx, `
		INSERT INTO mls_pending_welcomes (channel_id, recipient_user_id, sender_id, welcome_bytes, epoch, ciphersuite)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		channelID, recipientUserID, senderID, welcomeBytes, epoch, version.CurrentMLSCiphersuite,
	)
	return err
}

// GetPendingWelcomes returns all pending Welcome messages for a recipient user at
// the current server ciphersuite, ordered by created_at ascending (oldest first).
// Legacy-suite welcomes are filtered out: a client running the new protocol cannot
// process them and forwarding them would either error out or burn forward secrecy.
func (p *Pool) GetPendingWelcomes(ctx context.Context, recipientUserID string) ([]PendingWelcomeRow, error) {
	rows, err := p.Query(ctx, `
		SELECT id, channel_id, welcome_bytes, sender_id, epoch, created_at
		FROM mls_pending_welcomes
		WHERE recipient_user_id = $1
		  AND ciphersuite       = $2
		ORDER BY created_at ASC`,
		recipientUserID, version.CurrentMLSCiphersuite,
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

// DeletePendingWelcome removes a specific pending Welcome at the current server
// ciphersuite after the client ACKs it. Legacy-suite welcomes are not deletable
// through this path: a current-suite client has no way to obtain their ID
// (GetPendingWelcomes already filters them out), and a server-side janitor that
// wants to purge legacy welcomes wholesale should do so as an intentional
// migration, not through the per-row ACK endpoint.
func (p *Pool) DeletePendingWelcome(ctx context.Context, welcomeID string) error {
	_, err := p.Exec(ctx, `
		DELETE FROM mls_pending_welcomes
		WHERE id          = $1
		  AND ciphersuite = $2`,
		welcomeID, version.CurrentMLSCiphersuite,
	)
	return err
}

// GetVoiceKeyRotationHours returns the configured voice group periodic key rotation
// interval from instance_config. Returns the default of 2 when the row is missing.
func (p *Pool) GetVoiceKeyRotationHours(ctx context.Context) (int, error) {
	var hours int
	err := p.QueryRow(ctx, `
		SELECT voice_key_rotation_hours FROM instance_config LIMIT 1`).Scan(&hours)
	if errors.Is(err, pgx.ErrNoRows) {
		return 2, nil
	}
	if err != nil {
		return 0, err
	}
	return hours, nil
}

// UpsertMLSGuildMetadataGroupInfo inserts or updates the GroupInfo bytes for a guild
// metadata group (group_type = 'metadata', server_id scoped, channel_id = NULL) at
// the active server ciphersuite. A legacy-suite metadata row for the same guild is
// left intact; the partial unique index on (server_id, group_type, ciphersuite)
// keeps the two rows from colliding. On conflict at the current suite the
// group_info_bytes, epoch, and updated_at are refreshed.
func (p *Pool) UpsertMLSGuildMetadataGroupInfo(ctx context.Context, serverID string, groupInfoBytes []byte, epoch int64) error {
	_, err := p.Exec(ctx, `
		INSERT INTO mls_group_info (server_id, group_type, group_info_bytes, epoch, ciphersuite, updated_at)
		VALUES ($1, 'metadata', $2, $3, $4, now())
		ON CONFLICT (server_id, group_type, ciphersuite) WHERE server_id IS NOT NULL DO UPDATE SET
			group_info_bytes = EXCLUDED.group_info_bytes,
			epoch            = EXCLUDED.epoch,
			updated_at       = now()`,
		serverID, groupInfoBytes, epoch, version.CurrentMLSCiphersuite,
	)
	return err
}

// GetMLSGuildMetadataGroupInfo returns the GroupInfo bytes and epoch for a guild
// metadata group at the current server ciphersuite. Legacy-suite rows are never
// returned. Returns (nil, 0, nil) when no current-suite row exists.
func (p *Pool) GetMLSGuildMetadataGroupInfo(ctx context.Context, serverID string) (groupInfoBytes []byte, epoch int64, err error) {
	err = p.QueryRow(ctx, `
		SELECT group_info_bytes, epoch
		FROM mls_group_info
		WHERE server_id   = $1
		  AND group_type  = 'metadata'
		  AND ciphersuite = $2`,
		serverID, version.CurrentMLSCiphersuite,
	).Scan(&groupInfoBytes, &epoch)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil
	}
	return groupInfoBytes, epoch, err
}

// DeleteMLSGuildMetadataGroupInfo removes the metadata group row for the given
// guild at the current server ciphersuite. Called when a guild is deleted.
// Legacy-suite rows are preserved as audit history.
func (p *Pool) DeleteMLSGuildMetadataGroupInfo(ctx context.Context, serverID string) error {
	_, err := p.Exec(ctx, `
		DELETE FROM mls_group_info
		WHERE server_id   = $1
		  AND group_type  = 'metadata'
		  AND ciphersuite = $2`,
		serverID, version.CurrentMLSCiphersuite,
	)
	return err
}
