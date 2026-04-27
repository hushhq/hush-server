package db

import (
	"context"
	"errors"
	"time"

	"github.com/hushhq/hush-server/internal/models"

	"github.com/jackc/pgx/v5"
)

// CreateInstanceAdminSession stores a hashed cookie session for an instance admin.
func (p *Pool) CreateInstanceAdminSession(
	ctx context.Context,
	sessionID, adminID, tokenHash string,
	expiresAt time.Time,
	createdIP, userAgent *string,
) (*models.InstanceAdminSession, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO instance_admin_sessions (id, admin_id, token_hash, expires_at, created_ip, user_agent)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, admin_id, token_hash, expires_at, last_seen_at, created_ip, user_agent, created_at, revoked_at`,
		sessionID, adminID, tokenHash, expiresAt, createdIP, userAgent,
	)
	return scanInstanceAdminSession(row)
}

// GetInstanceAdminSessionByTokenHash returns the active admin session for the token hash.
func (p *Pool) GetInstanceAdminSessionByTokenHash(ctx context.Context, tokenHash string) (*models.InstanceAdminSession, error) {
	row := p.QueryRow(ctx, `
		SELECT id, admin_id, token_hash, expires_at, last_seen_at, created_ip, user_agent, created_at, revoked_at
		FROM instance_admin_sessions
		WHERE token_hash = $1
		  AND revoked_at IS NULL
		  AND expires_at > now()`,
		tokenHash,
	)
	session, err := scanInstanceAdminSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return session, err
}

// DeleteInstanceAdminSessionByID revokes the target admin session.
func (p *Pool) DeleteInstanceAdminSessionByID(ctx context.Context, sessionID string) error {
	_, err := p.Exec(ctx, `
		UPDATE instance_admin_sessions
		SET revoked_at = now()
		WHERE id = $1 AND revoked_at IS NULL`,
		sessionID,
	)
	return err
}

// PurgeStaleAdminSessions deletes admin-session rows whose expires_at is in
// the past, plus any revoked rows older than revokedRetention. Returns the
// number of rows removed.
func (p *Pool) PurgeStaleAdminSessions(ctx context.Context, revokedRetention time.Duration) (int64, error) {
	tag, err := p.Exec(ctx, `
		DELETE FROM instance_admin_sessions
		WHERE expires_at < now()
		   OR (revoked_at IS NOT NULL AND revoked_at < now() - $1::interval)`,
		revokedRetention,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// UpdateInstanceAdminSessionLastSeen records the latest request time for a session.
func (p *Pool) UpdateInstanceAdminSessionLastSeen(ctx context.Context, sessionID string, seenAt time.Time) error {
	_, err := p.Exec(ctx, `
		UPDATE instance_admin_sessions
		SET last_seen_at = $2
		WHERE id = $1 AND revoked_at IS NULL`,
		sessionID, seenAt,
	)
	return err
}

type instanceAdminSessionScanner interface {
	Scan(dest ...any) error
}

func scanInstanceAdminSession(row instanceAdminSessionScanner) (*models.InstanceAdminSession, error) {
	var session models.InstanceAdminSession
	if err := row.Scan(
		&session.ID,
		&session.AdminID,
		&session.TokenHash,
		&session.ExpiresAt,
		&session.LastSeenAt,
		&session.CreatedIP,
		&session.UserAgent,
		&session.CreatedAt,
		&session.RevokedAt,
	); err != nil {
		return nil, err
	}
	return &session, nil
}
