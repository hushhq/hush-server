package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"hush.app/server/internal/models"
)

// InsertBan creates a new ban record and returns the created row.
func (p *Pool) InsertBan(ctx context.Context, userID, actorID, reason string, expiresAt *time.Time) (*models.Ban, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO bans (user_id, actor_id, reason, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, actor_id, reason, expires_at, created_at, lifted_at, lifted_by`,
		userID, actorID, reason, expiresAt,
	)
	return scanBan(row)
}

// GetActiveBan returns the active ban for userID, or nil if none exists.
// A ban is active when lifted_at IS NULL and either expires_at IS NULL or expires_at > now().
func (p *Pool) GetActiveBan(ctx context.Context, userID string) (*models.Ban, error) {
	row := p.QueryRow(ctx, `
		SELECT id, user_id, actor_id, reason, expires_at, created_at, lifted_at, lifted_by
		FROM bans
		WHERE user_id = $1
		  AND lifted_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())
		ORDER BY created_at DESC
		LIMIT 1`,
		userID,
	)
	ban, err := scanBan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return ban, err
}

// LiftBan sets lifted_at = now() and lifted_by on the given ban record.
// Returns an error if the ban does not exist or is already lifted.
func (p *Pool) LiftBan(ctx context.Context, banID, liftedByID string) error {
	_, err := p.Exec(ctx, `
		UPDATE bans
		SET lifted_at = now(), lifted_by = $2
		WHERE id = $1 AND lifted_at IS NULL`,
		banID, liftedByID,
	)
	return err
}

// InsertMute creates a new mute record and returns the created row.
func (p *Pool) InsertMute(ctx context.Context, userID, actorID, reason string, expiresAt *time.Time) (*models.Mute, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO mutes (user_id, actor_id, reason, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, actor_id, reason, expires_at, created_at, lifted_at, lifted_by`,
		userID, actorID, reason, expiresAt,
	)
	return scanMute(row)
}

// GetActiveMute returns the active mute for userID, or nil if none exists.
// A mute is active when lifted_at IS NULL and either expires_at IS NULL or expires_at > now().
func (p *Pool) GetActiveMute(ctx context.Context, userID string) (*models.Mute, error) {
	row := p.QueryRow(ctx, `
		SELECT id, user_id, actor_id, reason, expires_at, created_at, lifted_at, lifted_by
		FROM mutes
		WHERE user_id = $1
		  AND lifted_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())
		ORDER BY created_at DESC
		LIMIT 1`,
		userID,
	)
	mute, err := scanMute(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return mute, err
}

// LiftMute sets lifted_at = now() and lifted_by on the given mute record.
// Returns an error if the mute does not exist or is already lifted.
func (p *Pool) LiftMute(ctx context.Context, muteID, liftedByID string) error {
	_, err := p.Exec(ctx, `
		UPDATE mutes
		SET lifted_at = now(), lifted_by = $2
		WHERE id = $1 AND lifted_at IS NULL`,
		muteID, liftedByID,
	)
	return err
}

// InsertAuditLog records a moderation action. metadata may be nil.
func (p *Pool) InsertAuditLog(ctx context.Context, actorID string, targetID *string, action, reason string, metadata map[string]interface{}) error {
	var metaJSON []byte
	if metadata != nil {
		var err error
		metaJSON, err = json.Marshal(metadata)
		if err != nil {
			return err
		}
	}
	_, err := p.Exec(ctx, `
		INSERT INTO audit_log (actor_id, target_id, action, reason, metadata)
		VALUES ($1, $2, $3, $4, $5)`,
		actorID, targetID, action, reason, metaJSON,
	)
	return err
}

// ListAuditLog returns audit log entries ordered by created_at DESC with pagination.
func (p *Pool) ListAuditLog(ctx context.Context, limit, offset int) ([]models.AuditLogEntry, error) {
	rows, err := p.Query(ctx, `
		SELECT id, actor_id, target_id, action, reason, metadata, created_at
		FROM audit_log
		ORDER BY created_at DESC
		LIMIT $1 OFFSET $2`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.AuditLogEntry
	for rows.Next() {
		var entry models.AuditLogEntry
		var metaBytes []byte
		if err := rows.Scan(&entry.ID, &entry.ActorID, &entry.TargetID, &entry.Action, &entry.Reason, &metaBytes, &entry.CreatedAt); err != nil {
			return nil, err
		}
		if metaBytes != nil {
			if err := json.Unmarshal(metaBytes, &entry.Metadata); err != nil {
				return nil, err
			}
		}
		out = append(out, entry)
	}
	return out, rows.Err()
}

// DeleteSessionsByUserID removes all active sessions for the given user.
// Used when kicking or banning a user to invalidate their current tokens.
func (p *Pool) DeleteSessionsByUserID(ctx context.Context, userID string) error {
	_, err := p.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return err
}

// scanBan scans a single row into a Ban struct.
func scanBan(row pgx.Row) (*models.Ban, error) {
	var b models.Ban
	err := row.Scan(&b.ID, &b.UserID, &b.ActorID, &b.Reason, &b.ExpiresAt, &b.CreatedAt, &b.LiftedAt, &b.LiftedBy)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// scanMute scans a single row into a Mute struct.
func scanMute(row pgx.Row) (*models.Mute, error) {
	var m models.Mute
	err := row.Scan(&m.ID, &m.UserID, &m.ActorID, &m.Reason, &m.ExpiresAt, &m.CreatedAt, &m.LiftedAt, &m.LiftedBy)
	if err != nil {
		return nil, err
	}
	return &m, nil
}
