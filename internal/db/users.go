package db

import (
	"context"
	"database/sql"
	"errors"

	"github.com/hushhq/hush-server/internal/models"

	"github.com/jackc/pgx/v5"
)

// ErrInstanceUserLimitReached is returned when instance_config.max_registered_users
// would be exceeded by creating a new persisted user.
var ErrInstanceUserLimitReached = errors.New("instance user registration limit reached")

// CreateUserWithPublicKey inserts a user with a BIP39 Ed25519 root public key
// and returns the created user with server-assigned ID. No password is stored.
func (p *Pool) CreateUserWithPublicKey(ctx context.Context, username, displayName string, publicKey []byte) (*models.User, error) {
	tx, err := p.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var maxRegisteredUsers sql.NullInt64
	err = tx.QueryRow(ctx, `
		SELECT max_registered_users
		FROM instance_config
		FOR UPDATE`).Scan(&maxRegisteredUsers)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if maxRegisteredUsers.Valid {
		var currentUsers int64
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&currentUsers); err != nil {
			return nil, err
		}
		if currentUsers >= maxRegisteredUsers.Int64 {
			return nil, ErrInstanceUserLimitReached
		}
	}

	row := tx.QueryRow(ctx, `
		INSERT INTO users (username, display_name, root_public_key)
		VALUES ($1, $2, $3)
		RETURNING id, username, root_public_key, display_name, role, created_at`,
		username, displayName, publicKey,
	)
	user, err := scanUser(row)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return user, nil
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
// Ed25519 public key, or (nil, nil) if not found.
func (p *Pool) GetUserByPublicKey(ctx context.Context, publicKey []byte) (*models.User, error) {
	row := p.QueryRow(ctx, `
		SELECT id, username, root_public_key, display_name, role, created_at
		FROM users WHERE root_public_key = $1`, publicKey)
	u, err := scanUser(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return u, err
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
