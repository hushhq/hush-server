package db

import (
	"context"

	"hush.app/server/internal/models"

	"github.com/jackc/pgx/v5"
)

// CreateUser inserts a user and returns the created user with ID.
func (p *Pool) CreateUser(ctx context.Context, username, displayName string, passwordHash *string) (*models.User, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO users (username, password_hash, display_name)
		VALUES ($1, $2, $3)
		RETURNING id, username, password_hash, display_name, created_at`,
		username, passwordHash, displayName,
	)
	return scanUser(row)
}

// GetUserByID returns the user by ID.
func (p *Pool) GetUserByID(ctx context.Context, id string) (*models.User, error) {
	row := p.QueryRow(ctx, `
		SELECT id, username, password_hash, display_name, created_at
		FROM users WHERE id = $1`, id)
	return scanUser(row)
}

// GetUserByUsername returns the user by username.
func (p *Pool) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	row := p.QueryRow(ctx, `
		SELECT id, username, password_hash, display_name, created_at
		FROM users WHERE username = $1`, username)
	return scanUser(row)
}

func scanUser(row pgx.Row) (*models.User, error) {
	var u models.User
	var passHash *string
	err := row.Scan(&u.ID, &u.Username, &passHash, &u.DisplayName, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	u.PasswordHash = passHash
	return &u, nil
}
