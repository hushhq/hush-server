package db

import (
	"context"

	"hush.app/server/internal/models"
)

// AddServerMember inserts a new guild membership record with the given permission level.
func (p *Pool) AddServerMember(ctx context.Context, serverID, userID string, permissionLevel int) error {
	_, err := p.Exec(ctx, `
		INSERT INTO server_members (server_id, user_id, permission_level)
		VALUES ($1, $2, $3)`,
		serverID, userID, permissionLevel,
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

// GetServerMemberLevel returns the permission_level of the user within the guild.
// Returns an error if the user is not a member.
func (p *Pool) GetServerMemberLevel(ctx context.Context, serverID, userID string) (int, error) {
	var level int
	err := p.QueryRow(ctx, `
		SELECT permission_level FROM server_members
		WHERE server_id = $1 AND user_id = $2`,
		serverID, userID,
	).Scan(&level)
	return level, err
}

// UpdateServerMemberLevel sets a new permission level for the given member.
func (p *Pool) UpdateServerMemberLevel(ctx context.Context, serverID, userID string, permissionLevel int) error {
	_, err := p.Exec(ctx, `
		UPDATE server_members SET permission_level = $3
		WHERE server_id = $1 AND user_id = $2`,
		serverID, userID, permissionLevel,
	)
	return err
}

// ListServerMembers returns all guild members (local and federated) with their profiles,
// ordered by join time. HomeInstance is nil for local users.
func (p *Pool) ListServerMembers(ctx context.Context, serverID string) ([]models.ServerMemberWithUser, error) {
	rows, err := p.Query(ctx, `
		SELECT
			COALESCE(u.id, fi.id) AS member_id,
			COALESCE(u.username, fi.username) AS username,
			COALESCE(u.display_name, fi.display_name) AS display_name,
			COALESCE(u.created_at, fi.cached_at) AS created_at,
			sm.permission_level,
			sm.joined_at,
			fi.home_instance
		FROM server_members sm
		LEFT JOIN users u ON u.id = sm.user_id
		LEFT JOIN federated_identities fi ON fi.id = sm.federated_identity_id
		WHERE sm.server_id = $1
		ORDER BY sm.joined_at`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.ServerMemberWithUser
	for rows.Next() {
		var m models.ServerMemberWithUser
		if err := rows.Scan(&m.ID, &m.Username, &m.DisplayName, &m.CreatedAt, &m.PermissionLevel, &m.JoinedAt, &m.HomeInstance); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AddFederatedServerMember inserts a guild membership record for a federated (foreign-instance) user.
func (p *Pool) AddFederatedServerMember(ctx context.Context, serverID, federatedIdentityID string, permissionLevel int) error {
	_, err := p.Exec(ctx, `
		INSERT INTO server_members (server_id, federated_identity_id, permission_level)
		VALUES ($1, $2, $3)`,
		serverID, federatedIdentityID, permissionLevel,
	)
	return err
}

// GetServerMemberLevelByFederatedID returns the permission_level for a federated guild member.
// Returns an error if the federated user is not a member.
func (p *Pool) GetServerMemberLevelByFederatedID(ctx context.Context, serverID, federatedIdentityID string) (int, error) {
	var level int
	err := p.QueryRow(ctx, `
		SELECT permission_level FROM server_members
		WHERE server_id = $1 AND federated_identity_id = $2`,
		serverID, federatedIdentityID,
	).Scan(&level)
	return level, err
}

// RemoveFederatedServerMember removes a federated user from the guild.
func (p *Pool) RemoveFederatedServerMember(ctx context.Context, serverID, federatedIdentityID string) error {
	_, err := p.Exec(ctx, `
		DELETE FROM server_members
		WHERE server_id = $1 AND federated_identity_id = $2`,
		serverID, federatedIdentityID,
	)
	return err
}
