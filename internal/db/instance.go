package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"hush.app/server/internal/models"
)

// GetInstanceConfig returns the single instance configuration row.
func (p *Pool) GetInstanceConfig(ctx context.Context) (*models.InstanceConfig, error) {
	row := p.QueryRow(ctx, `
		SELECT id, name, icon_url, owner_id, registration_mode, server_creation_policy, created_at
		FROM instance_config LIMIT 1`)
	var c models.InstanceConfig
	if err := row.Scan(&c.ID, &c.Name, &c.IconURL, &c.OwnerID, &c.RegistrationMode, &c.ServerCreationPolicy, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

// UpdateInstanceConfig updates only the non-nil fields of instance_config.
func (p *Pool) UpdateInstanceConfig(ctx context.Context, name *string, iconURL *string, registrationMode *string, serverCreationPolicy *string) error {
	if name == nil && iconURL == nil && registrationMode == nil && serverCreationPolicy == nil {
		return nil
	}
	setClauses := make([]string, 0, 4)
	args := make([]any, 0, 4)
	idx := 1
	if name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", idx))
		args = append(args, *name)
		idx++
	}
	if iconURL != nil {
		setClauses = append(setClauses, fmt.Sprintf("icon_url = $%d", idx))
		args = append(args, *iconURL)
		idx++
	}
	if registrationMode != nil {
		setClauses = append(setClauses, fmt.Sprintf("registration_mode = $%d", idx))
		args = append(args, *registrationMode)
		idx++
	}
	if serverCreationPolicy != nil {
		setClauses = append(setClauses, fmt.Sprintf("server_creation_policy = $%d", idx))
		args = append(args, *serverCreationPolicy)
		idx++
	}
	query := "UPDATE instance_config SET " + strings.Join(setClauses, ", ")
	_, err := p.Exec(ctx, query, args...)
	return err
}

// SetInstanceOwner sets owner_id only if it is currently NULL (first-register race-safe).
// Returns true if the update succeeded (this user became owner), false if already claimed.
func (p *Pool) SetInstanceOwner(ctx context.Context, userID string) (bool, error) {
	tag, err := p.Exec(ctx, `
		UPDATE instance_config SET owner_id = $1 WHERE owner_id IS NULL`, userID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// GetUserRole returns the effective role of the user by ID.
// If the user is the instance owner (instance_config.owner_id), "owner" is
// returned regardless of the users.role column — this handles pre-migration
// users whose role column was never updated.
func (p *Pool) GetUserRole(ctx context.Context, userID string) (string, error) {
	var role string
	err := p.QueryRow(ctx, `
		SELECT CASE WHEN ic.owner_id = u.id THEN 'owner' ELSE u.role END
		FROM users u CROSS JOIN instance_config ic
		WHERE u.id = $1`, userID).Scan(&role)
	return role, err
}

// UpdateUserRole sets the role for a user.
func (p *Pool) UpdateUserRole(ctx context.Context, userID, role string) error {
	_, err := p.Exec(ctx, `UPDATE users SET role = $1 WHERE id = $2`, role, userID)
	return err
}

// ListMembers returns all users ordered by creation time.
func (p *Pool) ListMembers(ctx context.Context) ([]models.Member, error) {
	rows, err := p.Query(ctx, `
		SELECT id, username, display_name, role, created_at
		FROM users ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Member
	for rows.Next() {
		var m models.Member
		if err := rows.Scan(&m.ID, &m.Username, &m.DisplayName, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []models.Member{}
	}
	return out, nil
}

// InsertInstanceBan creates a new instance-level ban record and returns the created row.
func (p *Pool) InsertInstanceBan(ctx context.Context, userID, actorID, reason string, expiresAt *time.Time) (*models.InstanceBan, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO instance_bans (user_id, actor_id, reason, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, actor_id, reason, expires_at, created_at, lifted_at, lifted_by`,
		userID, actorID, reason, expiresAt,
	)
	var b models.InstanceBan
	err := row.Scan(&b.ID, &b.UserID, &b.ActorID, &b.Reason, &b.ExpiresAt, &b.CreatedAt, &b.LiftedAt, &b.LiftedBy)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// GetActiveInstanceBan returns the active instance ban for userID, or nil if none exists.
// A ban is active when lifted_at IS NULL and either expires_at IS NULL or expires_at > now().
func (p *Pool) GetActiveInstanceBan(ctx context.Context, userID string) (*models.InstanceBan, error) {
	row := p.QueryRow(ctx, `
		SELECT id, user_id, actor_id, reason, expires_at, created_at, lifted_at, lifted_by
		FROM instance_bans
		WHERE user_id = $1
		  AND lifted_at IS NULL
		  AND (expires_at IS NULL OR expires_at > now())
		ORDER BY created_at DESC
		LIMIT 1`,
		userID,
	)
	var b models.InstanceBan
	err := row.Scan(&b.ID, &b.UserID, &b.ActorID, &b.Reason, &b.ExpiresAt, &b.CreatedAt, &b.LiftedAt, &b.LiftedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// LiftInstanceBan sets lifted_at = now() and lifted_by on the given instance ban record.
func (p *Pool) LiftInstanceBan(ctx context.Context, banID, liftedByID string) error {
	_, err := p.Exec(ctx, `
		UPDATE instance_bans
		SET lifted_at = now(), lifted_by = $2
		WHERE id = $1 AND lifted_at IS NULL`,
		banID, liftedByID,
	)
	return err
}

// InsertInstanceAuditLog records an instance-level admin action. metadata may be nil.
func (p *Pool) InsertInstanceAuditLog(ctx context.Context, actorID string, targetID *string, action, reason string, metadata map[string]interface{}) error {
	var metaJSON []byte
	if metadata != nil {
		var err error
		metaJSON, err = json.Marshal(metadata)
		if err != nil {
			return err
		}
	}
	_, err := p.Exec(ctx, `
		INSERT INTO instance_audit_log (actor_id, target_id, action, reason, metadata)
		VALUES ($1, $2, $3, $4, $5)`,
		actorID, targetID, action, reason, metaJSON,
	)
	return err
}

// ListInstanceAuditLog returns instance audit log entries ordered by created_at DESC with pagination.
// An optional filter may narrow results by action type and/or target ID.
func (p *Pool) ListInstanceAuditLog(ctx context.Context, limit, offset int, filter *InstanceAuditLogFilter) ([]models.InstanceAuditLogEntry, error) {
	query := `
		SELECT id, actor_id, target_id, action, reason, metadata, created_at
		FROM instance_audit_log
		WHERE 1=1`
	args := []interface{}{}
	paramIdx := 1

	if filter != nil {
		if filter.Action != "" {
			query += " AND action = $" + strconv.Itoa(paramIdx)
			args = append(args, filter.Action)
			paramIdx++
		}
		if filter.TargetID != "" {
			query += " AND target_id = $" + strconv.Itoa(paramIdx)
			args = append(args, filter.TargetID)
			paramIdx++
		}
	}

	query += " ORDER BY created_at DESC LIMIT $" + strconv.Itoa(paramIdx) + " OFFSET $" + strconv.Itoa(paramIdx+1)
	args = append(args, limit, offset)

	rows, err := p.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.InstanceAuditLogEntry
	for rows.Next() {
		var entry models.InstanceAuditLogEntry
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []models.InstanceAuditLogEntry{}
	}
	return out, nil
}

// SearchUsers searches for users by username prefix. Returns at most `limit` results (capped at 25).
// Includes active instance ban status via LEFT JOIN.
func (p *Pool) SearchUsers(ctx context.Context, query string, limit int) ([]models.UserSearchResult, error) {
	if limit <= 0 || limit > 25 {
		limit = 25
	}
	rows, err := p.Query(ctx, `
		SELECT u.id, u.username, u.display_name, u.role, u.created_at,
		       ib.id IS NOT NULL AS is_banned,
		       ib.reason AS ban_reason,
		       ib.expires_at AS ban_expires_at
		FROM users u
		LEFT JOIN instance_bans ib ON ib.user_id = u.id
		    AND ib.lifted_at IS NULL
		    AND (ib.expires_at IS NULL OR ib.expires_at > now())
		WHERE u.username ILIKE $1 || '%'
		ORDER BY u.username
		LIMIT $2`,
		query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []models.UserSearchResult
	for rows.Next() {
		var r models.UserSearchResult
		if err := rows.Scan(&r.ID, &r.Username, &r.DisplayName, &r.Role, &r.CreatedAt, &r.IsBanned, &r.BanReason, &r.BanExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []models.UserSearchResult{}
	}
	return out, nil
}
