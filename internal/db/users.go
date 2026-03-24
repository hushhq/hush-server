package db

import (
	"context"

	"hush.app/server/internal/models"

	"github.com/jackc/pgx/v5"
)

// CreateUserWithPublicKey inserts a user with a BIP39 Ed25519 root public key
// and returns the created user with server-assigned ID. No password is stored.
func (p *Pool) CreateUserWithPublicKey(ctx context.Context, username, displayName string, publicKey []byte) (*models.User, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO users (username, display_name, root_public_key)
		VALUES ($1, $2, $3)
		RETURNING id, username, root_public_key, display_name, role, created_at`,
		username, displayName, publicKey,
	)
	return scanUser(row)
}

// GetUserByID returns the user by UUID.
func (p *Pool) GetUserByID(ctx context.Context, id string) (*models.User, error) {
	row := p.QueryRow(ctx, `
		SELECT id, username, root_public_key, display_name, role, created_at
		FROM users WHERE id = $1`, id)
	return scanUser(row)
}

// GetUserByUsername returns the user by username.
func (p *Pool) GetUserByUsername(ctx context.Context, username string) (*models.User, error) {
	row := p.QueryRow(ctx, `
		SELECT id, username, root_public_key, display_name, role, created_at
		FROM users WHERE username = $1`, username)
	return scanUser(row)
}

// GetUserByPublicKey returns the user whose root_public_key matches the given
// Ed25519 public key, or pgx.ErrNoRows if not found.
func (p *Pool) GetUserByPublicKey(ctx context.Context, publicKey []byte) (*models.User, error) {
	row := p.QueryRow(ctx, `
		SELECT id, username, root_public_key, display_name, role, created_at
		FROM users WHERE root_public_key = $1`, publicKey)
	return scanUser(row)
}

// scanUser reads a user row that includes root_public_key.
func scanUser(row pgx.Row) (*models.User, error) {
	var u models.User
	err := row.Scan(&u.ID, &u.Username, &u.RootPublicKey, &u.DisplayName, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}
