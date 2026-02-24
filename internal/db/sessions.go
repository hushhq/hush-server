package db

import (
	"context"
	"time"

	"hush.app/server/internal/models"
)

// CreateSession inserts a session with the given id (for JWT session_id claim) and returns it.
func (p *Pool) CreateSession(ctx context.Context, sessionID, userID, tokenHash string, expiresAt time.Time) (*models.Session, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO sessions (id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, user_id, token_hash, expires_at`,
		sessionID, userID, tokenHash, expiresAt,
	)
	var s models.Session
	err := row.Scan(&s.ID, &s.UserID, &s.TokenHash, &s.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetSessionByTokenHash returns the session if it exists and is not expired.
func (p *Pool) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*models.Session, error) {
	row := p.QueryRow(ctx, `
		SELECT id, user_id, token_hash, expires_at
		FROM sessions
		WHERE token_hash = $1 AND expires_at > now()`,
		tokenHash,
	)
	var s models.Session
	err := row.Scan(&s.ID, &s.UserID, &s.TokenHash, &s.ExpiresAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// DeleteSessionByID removes the session (logout).
func (p *Pool) DeleteSessionByID(ctx context.Context, sessionID string) error {
	_, err := p.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, sessionID)
	return err
}
