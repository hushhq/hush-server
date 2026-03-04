package db

import (
	"context"
	"fmt"
	"strings"

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
