package db

import (
	"context"

	"hush.app/server/internal/models"
)

// AddServerMember inserts a new guild membership record.
func (p *Pool) AddServerMember(ctx context.Context, serverID, userID, role string) error {
	_, err := p.Exec(ctx, `
		INSERT INTO server_members (server_id, user_id, role)
		VALUES ($1, $2, $3)`,
		serverID, userID, role,
	)
	return err
}

// RemoveServerMember removes the user from the guild.
func (p *Pool) RemoveServerMember(ctx context.Context, serverID, userID string) error {
	_, err := p.Exec(ctx, `
		DELETE FROM server_members
		WHERE server_id = $1 AND user_id = $2`,
		serverID, userID,
	)
	return err
}

// GetServerMemberRole returns the role of the user within the guild.
// Returns an error if the user is not a member.
func (p *Pool) GetServerMemberRole(ctx context.Context, serverID, userID string) (string, error) {
	var role string
	err := p.QueryRow(ctx, `
		SELECT role FROM server_members
		WHERE server_id = $1 AND user_id = $2`,
		serverID, userID,
	).Scan(&role)
	return role, err
}

// UpdateServerMemberRole sets a new role for the given member.
func (p *Pool) UpdateServerMemberRole(ctx context.Context, serverID, userID, role string) error {
	_, err := p.Exec(ctx, `
		UPDATE server_members SET role = $1
		WHERE server_id = $2 AND user_id = $3`,
		role, serverID, userID,
	)
	return err
}

// ListServerMembers returns all guild members with their user profiles, ordered by join time.
func (p *Pool) ListServerMembers(ctx context.Context, serverID string) ([]models.ServerMemberWithUser, error) {
	rows, err := p.Query(ctx, `
		SELECT u.id, u.username, u.display_name, u.created_at, sm.role, sm.joined_at
		FROM server_members sm
		JOIN users u ON u.id = sm.user_id
		WHERE sm.server_id = $1
		ORDER BY sm.joined_at`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.ServerMemberWithUser
	for rows.Next() {
		var m models.ServerMemberWithUser
		if err := rows.Scan(&m.ID, &m.Username, &m.DisplayName, &m.CreatedAt, &m.Role, &m.JoinedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
