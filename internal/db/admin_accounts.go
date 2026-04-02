package db

import (
	"context"
	"errors"
	"time"

	"github.com/hushhq/hush-server/internal/models"

	"github.com/jackc/pgx/v5"
)

// CountInstanceAdmins returns the number of local instance-admin accounts.
func (p *Pool) CountInstanceAdmins(ctx context.Context) (int, error) {
	var count int
	if err := p.QueryRow(ctx, `SELECT COUNT(*) FROM instance_admins`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// CreateInstanceAdmin inserts a new local instance-admin account.
func (p *Pool) CreateInstanceAdmin(ctx context.Context, username string, email *string, passwordHash, role string) (*models.InstanceAdmin, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO instance_admins (username, email, password_hash, role)
		VALUES ($1, $2, $3, $4)
		RETURNING id, username, email, password_hash, role, is_active, last_login_at, created_at, updated_at`,
		username, email, passwordHash, role,
	)
	return scanInstanceAdmin(row)
}

// GetInstanceAdminByUsername returns an instance admin by username, or nil when absent.
func (p *Pool) GetInstanceAdminByUsername(ctx context.Context, username string) (*models.InstanceAdmin, error) {
	row := p.QueryRow(ctx, `
		SELECT id, username, email, password_hash, role, is_active, last_login_at, created_at, updated_at
		FROM instance_admins
		WHERE username = $1`,
		username,
	)
	admin, err := scanInstanceAdmin(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return admin, err
}

// GetInstanceAdminByID returns an instance admin by ID, or nil when absent.
func (p *Pool) GetInstanceAdminByID(ctx context.Context, id string) (*models.InstanceAdmin, error) {
	row := p.QueryRow(ctx, `
		SELECT id, username, email, password_hash, role, is_active, last_login_at, created_at, updated_at
		FROM instance_admins
		WHERE id = $1`,
		id,
	)
	admin, err := scanInstanceAdmin(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return admin, err
}

// ListInstanceAdmins returns all instance-admin accounts ordered by creation time.
func (p *Pool) ListInstanceAdmins(ctx context.Context) ([]models.InstanceAdmin, error) {
	rows, err := p.Query(ctx, `
		SELECT id, username, email, password_hash, role, is_active, last_login_at, created_at, updated_at
		FROM instance_admins
		ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	admins := []models.InstanceAdmin{}
	for rows.Next() {
		admin, err := scanInstanceAdmin(rows)
		if err != nil {
			return nil, err
		}
		admins = append(admins, *admin)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return admins, nil
}

// UpdateInstanceAdmin updates mutable fields for an instance-admin account.
func (p *Pool) UpdateInstanceAdmin(ctx context.Context, id string, email *string, role string, isActive bool) (*models.InstanceAdmin, error) {
	row := p.QueryRow(ctx, `
		UPDATE instance_admins
		SET email = $2, role = $3, is_active = $4, updated_at = now()
		WHERE id = $1
		RETURNING id, username, email, password_hash, role, is_active, last_login_at, created_at, updated_at`,
		id, email, role, isActive,
	)
	admin, err := scanInstanceAdmin(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return admin, err
}

// UpdateInstanceAdminPassword replaces the password hash for the target admin.
func (p *Pool) UpdateInstanceAdminPassword(ctx context.Context, id, passwordHash string) error {
	_, err := p.Exec(ctx, `
		UPDATE instance_admins
		SET password_hash = $2, updated_at = now()
		WHERE id = $1`,
		id, passwordHash,
	)
	return err
}

// TouchInstanceAdminLastLogin records the latest successful login time.
func (p *Pool) TouchInstanceAdminLastLogin(ctx context.Context, id string, loginAt time.Time) error {
	_, err := p.Exec(ctx, `
		UPDATE instance_admins
		SET last_login_at = $2, updated_at = now()
		WHERE id = $1`,
		id, loginAt,
	)
	return err
}

type instanceAdminScanner interface {
	Scan(dest ...any) error
}

func scanInstanceAdmin(row instanceAdminScanner) (*models.InstanceAdmin, error) {
	var admin models.InstanceAdmin
	if err := row.Scan(
		&admin.ID,
		&admin.Username,
		&admin.Email,
		&admin.PasswordHash,
		&admin.Role,
		&admin.IsActive,
		&admin.LastLoginAt,
		&admin.CreatedAt,
		&admin.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &admin, nil
}
