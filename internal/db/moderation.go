package db

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/hushhq/hush-server/internal/models"
)

// InsertBan creates a new ban record scoped to the given guild and returns the created row.
func (p *Pool) InsertBan(ctx context.Context, serverID, userID, actorID, reason string, expiresAt *time.Time) (*models.Ban, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO bans (server_id, user_id, actor_id, reason, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, server_id, user_id, actor_id, reason, expires_at, created_at, lifted_at, lifted_by`,
		serverID, userID, actorID, reason, expiresAt,
	)
	return scanBan(row)
}

// GetActiveBan returns the active ban for userID within the given guild, or nil if none exists.
// A ban is active when lifted_at IS NULL and either expires_at IS NULL or expires_at > now().
func (p *Pool) GetActiveBan(ctx context.Context, serverID, userID string) (*models.Ban, error) {
	row := p.QueryRow(ctx, `
		SELECT id, server_id, user_id, actor_id, reason, expires_at, created_at, lifted_at, lifted_by
		FROM bans
		WHERE server_id = $1
		  AND user_id = $2
		  AND lifted_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())
		ORDER BY created_at DESC
		LIMIT 1`,
		serverID, userID,
	)
	ban, err := scanBan(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return ban, err
}

// ListActiveBans returns all active (non-lifted, non-expired) bans for the given guild,
// ordered by created_at DESC.
func (p *Pool) ListActiveBans(ctx context.Context, serverID string) ([]models.Ban, error) {
	rows, err := p.Query(ctx, `
		SELECT id, server_id, user_id, actor_id, reason, expires_at, created_at, lifted_at, lifted_by
		FROM bans
		WHERE server_id = $1
		  AND lifted_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())
		ORDER BY created_at DESC`,
		serverID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var bans []models.Ban
	for rows.Next() {
		var b models.Ban
		if err := rows.Scan(&b.ID, &b.ServerID, &b.UserID, &b.ActorID, &b.Reason, &b.ExpiresAt, &b.CreatedAt, &b.LiftedAt, &b.LiftedBy); err != nil {
			return nil, err
		}
		bans = append(bans, b)
	}
	return bans, rows.Err()
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

// InsertMute creates a new mute record scoped to the given guild and returns the created row.
func (p *Pool) InsertMute(ctx context.Context, serverID, userID, actorID, reason string, expiresAt *time.Time) (*models.Mute, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO mutes (server_id, user_id, actor_id, reason, expires_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, server_id, user_id, actor_id, reason, expires_at, created_at, lifted_at, lifted_by`,
		serverID, userID, actorID, reason, expiresAt,
	)
	return scanMute(row)
}

// GetActiveMute returns the active mute for userID within the given guild, or nil if none exists.
// A mute is active when lifted_at IS NULL and either expires_at IS NULL or expires_at > now().
func (p *Pool) GetActiveMute(ctx context.Context, serverID, userID string) (*models.Mute, error) {
	row := p.QueryRow(ctx, `
		SELECT id, server_id, user_id, actor_id, reason, expires_at, created_at, lifted_at, lifted_by
		FROM mutes
		WHERE server_id = $1
		  AND user_id = $2
		  AND lifted_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())
		ORDER BY created_at DESC
		LIMIT 1`,
		serverID, userID,
	)
	mute, err := scanMute(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return mute, err
}

// ListActiveMutes returns all active (non-lifted, non-expired) mutes for the given guild,
// ordered by created_at DESC.
func (p *Pool) ListActiveMutes(ctx context.Context, serverID string) ([]models.Mute, error) {
	rows, err := p.Query(ctx, `
		SELECT id, server_id, user_id, actor_id, reason, expires_at, created_at, lifted_at, lifted_by
		FROM mutes
		WHERE server_id = $1
		  AND lifted_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())
		ORDER BY created_at DESC`,
		serverID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var mutes []models.Mute
	for rows.Next() {
		var m models.Mute
		if err := rows.Scan(&m.ID, &m.ServerID, &m.UserID, &m.ActorID, &m.Reason, &m.ExpiresAt, &m.CreatedAt, &m.LiftedAt, &m.LiftedBy); err != nil {
			return nil, err
		}
		mutes = append(mutes, m)
	}
	return mutes, rows.Err()
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

// InsertAuditLog records a moderation action scoped to the given guild. metadata may be nil.
func (p *Pool) InsertAuditLog(ctx context.Context, serverID, actorID string, targetID *string, action, reason string, metadata map[string]interface{}) error {
	var metaJSON []byte
	if metadata != nil {
		var err error
		metaJSON, err = json.Marshal(metadata)
		if err != nil {
			return err
		}
	}
	_, err := p.Exec(ctx, `
		INSERT INTO audit_log (server_id, actor_id, target_id, action, reason, metadata)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		serverID, actorID, targetID, action, reason, metaJSON,
	)
	return err
}

// AuditLogFilter holds optional filter parameters for ListAuditLog.
type AuditLogFilter struct {
	Action   string // Filter by action type (e.g. "kick", "ban", "mute"). Empty = no filter.
	ActorID  string // Filter by actor user ID. Empty = no filter.
	TargetID string // Filter by target user ID. Empty = no filter.
}

// ListAuditLog returns audit log entries for the given guild ordered by created_at DESC with pagination.
// An optional filter may narrow results by action type, actor ID, and/or target ID.
func (p *Pool) ListAuditLog(ctx context.Context, serverID string, limit, offset int, filter *AuditLogFilter) ([]models.AuditLogEntry, error) {
	query := `
		SELECT id, server_id, actor_id, target_id, action, reason, metadata, created_at
		FROM audit_log
		WHERE server_id = $1`
	args := []interface{}{serverID}
	paramIdx := 2

	if filter != nil {
		if filter.Action != "" {
			query += " AND action = $" + itoa(paramIdx)
			args = append(args, filter.Action)
			paramIdx++
		}
		if filter.ActorID != "" {
			query += " AND actor_id = $" + itoa(paramIdx)
			args = append(args, filter.ActorID)
			paramIdx++
		}
		if filter.TargetID != "" {
			query += " AND target_id = $" + itoa(paramIdx)
			args = append(args, filter.TargetID)
			paramIdx++
		}
	}

	query += " ORDER BY created_at DESC LIMIT $" + itoa(paramIdx) + " OFFSET $" + itoa(paramIdx+1)
	args = append(args, limit, offset)

	rows, err := p.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.AuditLogEntry
	for rows.Next() {
		var entry models.AuditLogEntry
		var metaBytes []byte
		if err := rows.Scan(&entry.ID, &entry.ServerID, &entry.ActorID, &entry.TargetID, &entry.Action, &entry.Reason, &metaBytes, &entry.CreatedAt); err != nil {
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

// itoa converts an int to its decimal string representation.
// Avoids importing strconv in the moderation package.
func itoa(n int) string {
	return strconv.Itoa(n)
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
	err := row.Scan(&b.ID, &b.ServerID, &b.UserID, &b.ActorID, &b.Reason, &b.ExpiresAt, &b.CreatedAt, &b.LiftedAt, &b.LiftedBy)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// scanMute scans a single row into a Mute struct.
func scanMute(row pgx.Row) (*models.Mute, error) {
	var m models.Mute
	err := row.Scan(&m.ID, &m.ServerID, &m.UserID, &m.ActorID, &m.Reason, &m.ExpiresAt, &m.CreatedAt, &m.LiftedAt, &m.LiftedBy)
	if err != nil {
		return nil, err
	}
	return &m, nil
}
