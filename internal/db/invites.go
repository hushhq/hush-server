package db

import (
	"context"
	"errors"
	"time"

	"github.com/hushhq/hush-server/internal/models"

	"github.com/jackc/pgx/v5"
)

// CreateInvite inserts a new invite code scoped to the given guild.
func (p *Pool) CreateInvite(ctx context.Context, serverID, code, createdBy string, maxUses int, expiresAt time.Time) (*models.InviteCode, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO invite_codes (server_id, code, created_by, max_uses, uses, expires_at)
		VALUES ($1, $2, $3, $4, 0, $5)
		RETURNING code, server_id, created_by, expires_at, max_uses, uses`,
		serverID, code, createdBy, maxUses, expiresAt)
	return scanInviteCode(row)
}

// GetInviteByCode returns the invite by code, or nil if not found.
func (p *Pool) GetInviteByCode(ctx context.Context, code string) (*models.InviteCode, error) {
	row := p.QueryRow(ctx, `
		SELECT code, server_id, created_by, expires_at, max_uses, uses
		FROM invite_codes WHERE code = $1`, code)
	inv, err := scanInviteCode(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return inv, nil
}

// ClaimInviteUse atomically increments uses if the invite is still valid (not expired, under max).
// Returns true if the use was claimed, false if the invite is exhausted or expired.
func (p *Pool) ClaimInviteUse(ctx context.Context, code string) (bool, error) {
	tag, err := p.Exec(ctx, `
		UPDATE invite_codes SET uses = uses + 1
		WHERE code = $1 AND uses < max_uses AND expires_at > NOW()`, code)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func scanInviteCode(row pgx.Row) (*models.InviteCode, error) {
	var inv models.InviteCode
	err := row.Scan(&inv.Code, &inv.ServerID, &inv.CreatedBy, &inv.ExpiresAt, &inv.MaxUses, &inv.Uses)
	if err != nil {
		return nil, err
	}
	return &inv, nil
}
