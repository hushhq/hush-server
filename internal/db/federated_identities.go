package db

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/hushhq/hush-server/internal/models"
)

// GetOrCreateFederatedIdentity upserts a foreign-instance user by their Ed25519
// public key. On conflict it refreshes the username, display_name, and cached_at.
// Returns the full FederatedIdentity row (including server-assigned ID).
func (p *Pool) GetOrCreateFederatedIdentity(
	ctx context.Context,
	publicKey []byte,
	homeInstance, username, displayName string,
) (*models.FederatedIdentity, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO federated_identities (public_key, home_instance, username, display_name)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (public_key) DO UPDATE
			SET username     = EXCLUDED.username,
			    display_name = EXCLUDED.display_name,
			    cached_at    = now()
		RETURNING id, public_key, home_instance, username, display_name, cached_at`,
		publicKey, homeInstance, username, displayName,
	)
	return scanFederatedIdentity(row)
}

// GetFederatedIdentityByPublicKey returns the cached foreign-instance user whose
// public_key matches, or (nil, nil) when no such row exists.
func (p *Pool) GetFederatedIdentityByPublicKey(
	ctx context.Context,
	publicKey []byte,
) (*models.FederatedIdentity, error) {
	row := p.QueryRow(ctx, `
		SELECT id, public_key, home_instance, username, display_name, cached_at
		FROM federated_identities
		WHERE public_key = $1`,
		publicKey,
	)
	fi, err := scanFederatedIdentity(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return fi, err
}

// GetFederatedIdentityByID returns the federated identity by primary-key ID,
// or (nil, nil) when no such row exists.
func (p *Pool) GetFederatedIdentityByID(ctx context.Context, id string) (*models.FederatedIdentity, error) {
	row := p.QueryRow(ctx, `
		SELECT id, public_key, home_instance, username, display_name, cached_at
		FROM federated_identities
		WHERE id = $1`, id)
	fi, err := scanFederatedIdentity(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return fi, err
}

// UpdateFederatedIdentityProfile refreshes the username, display_name, and
// cached_at for an existing federated identity row.
func (p *Pool) UpdateFederatedIdentityProfile(
	ctx context.Context,
	id, username, displayName string,
) error {
	_, err := p.Exec(ctx, `
		UPDATE federated_identities
		SET username     = $2,
		    display_name = $3,
		    cached_at    = now()
		WHERE id = $1`,
		id, username, displayName,
	)
	return err
}

// scanFederatedIdentity reads a single FederatedIdentity from a pgx.Row.
func scanFederatedIdentity(row pgx.Row) (*models.FederatedIdentity, error) {
	var fi models.FederatedIdentity
	err := row.Scan(
		&fi.ID,
		&fi.PublicKey,
		&fi.HomeInstance,
		&fi.Username,
		&fi.DisplayName,
		&fi.CachedAt,
	)
	if err != nil {
		return nil, err
	}
	return &fi, nil
}
