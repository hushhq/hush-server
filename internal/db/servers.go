package db

import (
	"context"
	"errors"
	"time"

	"hush.app/server/internal/models"

	"github.com/jackc/pgx/v5"
)

// CreateServerWithOwner inserts a server and adds the owner as admin in a single transaction.
func (p *Pool) CreateServerWithOwner(ctx context.Context, name string, iconURL *string, ownerID string) (*models.Server, error) {
	tx, err := p.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
		INSERT INTO servers (name, icon_url, owner_id)
		VALUES ($1, $2, $3)
		RETURNING id, name, icon_url, owner_id, created_at`,
		name, iconURL, ownerID,
	)
	s, err := scanServer(row)
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO server_members (server_id, user_id, role)
		VALUES ($1, $2, 'admin')`, s.ID, ownerID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// GetServerByID returns the server by ID, or nil if not found.
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

// ListServersForUser returns all servers the user is a member of, with their role.
func (p *Pool) ListServersForUser(ctx context.Context, userID string) ([]models.ServerWithRole, error) {
	rows, err := p.Query(ctx, `
		SELECT s.id, s.name, s.icon_url, s.owner_id, s.created_at, sm.role
		FROM servers s
		INNER JOIN server_members sm ON sm.server_id = s.id AND sm.user_id = $1
		ORDER BY s.created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.ServerWithRole
	for rows.Next() {
		var s models.Server
		var role string
		if err := rows.Scan(&s.ID, &s.Name, &s.IconURL, &s.OwnerID, &s.CreatedAt, &role); err != nil {
			return nil, err
		}
		out = append(out, models.ServerWithRole{Server: s, Role: role})
	}
	return out, rows.Err()
}

// UpdateServer updates name and/or icon_url. Nil means leave unchanged.
func (p *Pool) UpdateServer(ctx context.Context, serverID string, name *string, iconURL *string) error {
	_, err := p.Exec(ctx, `
		UPDATE servers SET name = COALESCE($2, name), icon_url = COALESCE($3, icon_url)
		WHERE id = $1`, serverID, name, iconURL)
	return err
}

// DeleteServer deletes the server. Cascades to members, channels, invite_codes.
func (p *Pool) DeleteServer(ctx context.Context, serverID string) error {
	_, err := p.Exec(ctx, `DELETE FROM servers WHERE id = $1`, serverID)
	return err
}

// AddServerMember adds a user to a server with the given role.
func (p *Pool) AddServerMember(ctx context.Context, serverID, userID, role string) error {
	_, err := p.Exec(ctx, `
		INSERT INTO server_members (server_id, user_id, role)
		VALUES ($1, $2, $3)`, serverID, userID, role)
	return err
}

// RemoveServerMember removes a user from a server.
func (p *Pool) RemoveServerMember(ctx context.Context, serverID, userID string) error {
	_, err := p.Exec(ctx, `
		DELETE FROM server_members WHERE server_id = $1 AND user_id = $2`,
		serverID, userID)
	return err
}

// GetServerMember returns the membership row, or nil if not a member.
func (p *Pool) GetServerMember(ctx context.Context, serverID, userID string) (*models.ServerMember, error) {
	row := p.QueryRow(ctx, `
		SELECT server_id, user_id, role, joined_at
		FROM server_members WHERE server_id = $1 AND user_id = $2`,
		serverID, userID)
	m, err := scanServerMember(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return m, nil
}

// TransferServerOwnership sets the server's owner_id to newOwnerID.
func (p *Pool) TransferServerOwnership(ctx context.Context, serverID, newOwnerID string) error {
	_, err := p.Exec(ctx, `UPDATE servers SET owner_id = $2 WHERE id = $1`, serverID, newOwnerID)
	return err
}

// UpdateServerMemberRole sets a member's role.
func (p *Pool) UpdateServerMemberRole(ctx context.Context, serverID, userID, role string) error {
	_, err := p.Exec(ctx, `UPDATE server_members SET role = $3 WHERE server_id = $1 AND user_id = $2`, serverID, userID, role)
	return err
}

// CountServerMembers returns the number of members in the server.
func (p *Pool) CountServerMembers(ctx context.Context, serverID string) (int, error) {
	var n int
	err := p.QueryRow(ctx, `SELECT COUNT(*) FROM server_members WHERE server_id = $1`, serverID).Scan(&n)
	return n, err
}

// GetNextOwnerCandidate returns one member to transfer ownership to: prefer admin, then mod, then oldest member.
func (p *Pool) GetNextOwnerCandidate(ctx context.Context, serverID, excludeUserID string) (*models.ServerMember, error) {
	row := p.QueryRow(ctx, `
		SELECT server_id, user_id, role, joined_at
		FROM server_members
		WHERE server_id = $1 AND user_id != $2
		ORDER BY CASE role WHEN 'admin' THEN 0 WHEN 'mod' THEN 1 ELSE 2 END, joined_at ASC
		LIMIT 1`, serverID, excludeUserID)
	m, err := scanServerMember(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return m, nil
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

func scanServer(row pgx.Row) (*models.Server, error) {
	var s models.Server
	err := row.Scan(&s.ID, &s.Name, &s.IconURL, &s.OwnerID, &s.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func scanServerMember(row pgx.Row) (*models.ServerMember, error) {
	var m models.ServerMember
	var joinedAt time.Time
	err := row.Scan(&m.ServerID, &m.UserID, &m.Role, &joinedAt)
	if err != nil {
		return nil, err
	}
	m.JoinedAt = joinedAt
	return &m, nil
}

func scanInviteCode(row pgx.Row) (*models.InviteCode, error) {
	var inv models.InviteCode
	err := row.Scan(&inv.Code, &inv.ServerID, &inv.CreatedBy, &inv.ExpiresAt, &inv.MaxUses, &inv.Uses)
	if err != nil {
		return nil, err
	}
	return &inv, nil
}
