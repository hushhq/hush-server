package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"hush.app/server/internal/models"
)

// CreateServer inserts a new guild and returns the created row.
func (p *Pool) CreateServer(ctx context.Context, name, ownerID string) (*models.Server, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO servers (name, owner_id)
		VALUES ($1, $2)
		RETURNING id, name, icon_url, owner_id, created_at`,
		name, ownerID,
	)
	return scanServer(row)
}

// GetServerByID returns the guild by ID, or nil if not found.
func (p *Pool) GetServerByID(ctx context.Context, serverID string) (*models.Server, error) {
	row := p.QueryRow(ctx, `
		SELECT id, name, icon_url, owner_id, created_at
		FROM servers WHERE id = $1`, serverID)
	s, err := scanServer(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return s, nil
}

// ListServersForUser returns all guilds the user is a member of, ordered by creation time.
func (p *Pool) ListServersForUser(ctx context.Context, userID string) ([]models.Server, error) {
	rows, err := p.Query(ctx, `
		SELECT s.id, s.name, s.icon_url, s.owner_id, s.created_at
		FROM servers s
		JOIN server_members sm ON sm.server_id = s.id
		WHERE sm.user_id = $1
		ORDER BY s.created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Server
	for rows.Next() {
		s, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// DeleteServer removes the guild by ID.
func (p *Pool) DeleteServer(ctx context.Context, serverID string) error {
	_, err := p.Exec(ctx, `DELETE FROM servers WHERE id = $1`, serverID)
	return err
}

// ListGuildBillingStats returns minimal infrastructure metadata for all guilds.
// Intended for the instance operator billing endpoint only — exposes no guild content.
// storage_bytes is 0 for MVP (no file upload tracking yet).
func (p *Pool) ListGuildBillingStats(ctx context.Context) ([]models.GuildBillingStats, error) {
	rows, err := p.Query(ctx, `
		SELECT
			s.id,
			(SELECT COUNT(*) FROM server_members WHERE server_id = s.id)::int AS member_count,
			0::bigint AS storage_bytes,
			s.owner_id,
			s.created_at
		FROM servers s
		ORDER BY s.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.GuildBillingStats
	for rows.Next() {
		var g models.GuildBillingStats
		if err := rows.Scan(&g.ID, &g.MemberCount, &g.StorageBytes, &g.OwnerID, &g.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func scanServer(row pgx.Row) (*models.Server, error) {
	var s models.Server
	err := row.Scan(&s.ID, &s.Name, &s.IconURL, &s.OwnerID, &s.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}
