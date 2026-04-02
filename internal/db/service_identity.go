package db

import (
	"context"
	"errors"

	"github.com/hushhq/hush-server/internal/models"

	"github.com/jackc/pgx/v5"
)

// GetInstanceServiceIdentity returns the stored technical service identity, or nil when absent.
func (p *Pool) GetInstanceServiceIdentity(ctx context.Context) (*models.InstanceServiceIdentity, error) {
	row := p.QueryRow(ctx, `
		SELECT id, username, public_key, wrapped_private_key, wrapping_key_version, created_at, updated_at
		FROM instance_service_identity
		LIMIT 1`)
	identity, err := scanInstanceServiceIdentity(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return identity, err
}

// UpsertInstanceServiceIdentity stores or replaces the single technical service identity row.
func (p *Pool) UpsertInstanceServiceIdentity(
	ctx context.Context,
	username string,
	publicKey, wrappedPrivateKey []byte,
	wrappingKeyVersion string,
) (*models.InstanceServiceIdentity, error) {
	row := p.QueryRow(ctx, `
		INSERT INTO instance_service_identity (username, public_key, wrapped_private_key, wrapping_key_version)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (username) DO UPDATE
		SET public_key = EXCLUDED.public_key,
		    wrapped_private_key = EXCLUDED.wrapped_private_key,
		    wrapping_key_version = EXCLUDED.wrapping_key_version,
		    updated_at = now()
		RETURNING id, username, public_key, wrapped_private_key, wrapping_key_version, created_at, updated_at`,
		username, publicKey, wrappedPrivateKey, wrappingKeyVersion,
	)
	return scanInstanceServiceIdentity(row)
}

type instanceServiceIdentityScanner interface {
	Scan(dest ...any) error
}

func scanInstanceServiceIdentity(row instanceServiceIdentityScanner) (*models.InstanceServiceIdentity, error) {
	var identity models.InstanceServiceIdentity
	if err := row.Scan(
		&identity.ID,
		&identity.Username,
		&identity.PublicKey,
		&identity.WrappedPrivateKey,
		&identity.WrappingKeyVersion,
		&identity.CreatedAt,
		&identity.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &identity, nil
}
