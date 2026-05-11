package db

import (
	"context"
	"errors"
	"time"

	"github.com/hushhq/hush-server/internal/version"
	"github.com/jackc/pgx/v5"
)

// UpsertMLSCredential inserts or replaces the MLS credential for a user+device.
// On conflict (user_id, device_id) the credential bytes, signing public key,
// identity version, and updated_at timestamp are all refreshed.
func (p *Pool) UpsertMLSCredential(ctx context.Context, userID, deviceID string, credentialBytes, signingPublicKey []byte, identityVersion int) error {
	_, err := p.Exec(ctx, `
		INSERT INTO mls_credentials
			(user_id, device_id, credential_bytes, signing_pub_key, identity_version, updated_at)
		VALUES ($1, $2, $3, $4, $5, now())
		ON CONFLICT (user_id, device_id) DO UPDATE SET
			credential_bytes = EXCLUDED.credential_bytes,
			signing_pub_key  = EXCLUDED.signing_pub_key,
			identity_version = EXCLUDED.identity_version,
			updated_at       = now()`,
		userID, deviceID, credentialBytes, signingPublicKey, identityVersion,
	)
	return err
}

// GetMLSCredential returns the stored MLS credential for a user+device.
// Returns (nil, nil, 0, nil) when no row exists.
func (p *Pool) GetMLSCredential(ctx context.Context, userID, deviceID string) (credentialBytes, signingPublicKey []byte, identityVersion int, err error) {
	err = p.QueryRow(ctx, `
		SELECT credential_bytes, signing_pub_key, identity_version
		FROM mls_credentials
		WHERE user_id = $1 AND device_id = $2`,
		userID, deviceID,
	).Scan(&credentialBytes, &signingPublicKey, &identityVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, 0, nil
	}
	return credentialBytes, signingPublicKey, identityVersion, err
}

// InsertMLSKeyPackages stores a batch of opaque KeyPackage blobs for a user+device.
// All packages in the batch share the same expiry time and are stamped with the
// active server ciphersuite (version.CurrentMLSCiphersuite); the delivery service
// will only hand these packages back out to consumers running the same suite.
func (p *Pool) InsertMLSKeyPackages(ctx context.Context, userID, deviceID string, packages [][]byte, expiresAt time.Time) error {
	if len(packages) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, kp := range packages {
		batch.Queue(`
			INSERT INTO mls_key_packages (user_id, device_id, key_package_bytes, expires_at, ciphersuite)
			VALUES ($1, $2, $3, $4, $5)`,
			userID, deviceID, kp, expiresAt, version.CurrentMLSCiphersuite,
		)
	}
	br := p.SendBatch(ctx, batch)
	defer br.Close()
	for range packages {
		if _, err := br.Exec(); err != nil {
			return err
		}
	}
	return nil
}

// InsertMLSLastResortKeyPackage replaces the existing last-resort KeyPackage for a
// user+device under the active ciphersuite with the new one. The last-resort
// package uses a far-future expiry (year 2099) so it is never purged by the
// cleanup job. Last-resort packages from previous ciphersuites are intentionally
// left in place: they are invisible to consume/count under the current suite
// and serve only as audit history until the operator chooses to purge them.
func (p *Pool) InsertMLSLastResortKeyPackage(ctx context.Context, userID, deviceID string, keyPackageBytes []byte) error {
	tx, err := p.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Remove any previous last-resort package for this device at the current
	// suite. Legacy-suite last-resort packages are not touched here.
	_, err = tx.Exec(ctx, `
		DELETE FROM mls_key_packages
		WHERE user_id     = $1
		  AND device_id   = $2
		  AND last_resort = true
		  AND ciphersuite = $3`,
		userID, deviceID, version.CurrentMLSCiphersuite,
	)
	if err != nil {
		return err
	}

	// Far-future expiry: last-resort packages are never auto-deleted.
	farFuture := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = tx.Exec(ctx, `
		INSERT INTO mls_key_packages (user_id, device_id, key_package_bytes, last_resort, expires_at, ciphersuite)
		VALUES ($1, $2, $3, true, $4, $5)`,
		userID, deviceID, keyPackageBytes, farFuture, version.CurrentMLSCiphersuite,
	)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// ConsumeMLSKeyPackage atomically marks one unused, non-expired, non-last-resort
// KeyPackage as consumed and returns its bytes. Only packages stamped with the
// current server ciphersuite are eligible; legacy-suite rows are invisible to
// this query so a client running the current protocol never receives state from
// an incompatible suite. If no regular packages remain, the call falls back to
// the last-resort package for the same suite (without marking it consumed - it
// is reusable). Returns (nil, nil) when no eligible package is available.
func (p *Pool) ConsumeMLSKeyPackage(ctx context.Context, userID, deviceID string) ([]byte, error) {
	// Attempt to atomically consume a regular package.
	var kpBytes []byte
	err := p.QueryRow(ctx, `
		UPDATE mls_key_packages
		SET consumed_at = now()
		WHERE id = (
			SELECT id FROM mls_key_packages
			WHERE user_id    = $1
			  AND device_id  = $2
			  AND ciphersuite = $3
			  AND consumed_at IS NULL
			  AND last_resort = false
			  AND expires_at > now()
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING key_package_bytes`,
		userID, deviceID, version.CurrentMLSCiphersuite,
	).Scan(&kpBytes)
	if err == nil {
		return kpBytes, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	// No regular package available - fall back to last-resort (read-only, reusable).
	err = p.QueryRow(ctx, `
		SELECT key_package_bytes FROM mls_key_packages
		WHERE user_id     = $1
		  AND device_id   = $2
		  AND ciphersuite = $3
		  AND last_resort = true
		  AND consumed_at IS NULL
		LIMIT 1`,
		userID, deviceID, version.CurrentMLSCiphersuite,
	).Scan(&kpBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return kpBytes, err
}

// CountUnusedMLSKeyPackages returns the number of unconsumed, non-expired,
// non-last-resort KeyPackages stamped with the current server ciphersuite for
// a user+device. Legacy-suite rows are excluded so the low-watermark event and
// client replenishment logic only react to packages that are actually usable
// under the active protocol epoch.
func (p *Pool) CountUnusedMLSKeyPackages(ctx context.Context, userID, deviceID string) (int, error) {
	var n int
	err := p.QueryRow(ctx, `
		SELECT COUNT(*) FROM mls_key_packages
		WHERE user_id     = $1
		  AND device_id   = $2
		  AND ciphersuite = $3
		  AND consumed_at IS NULL
		  AND last_resort = false
		  AND expires_at > now()`,
		userID, deviceID, version.CurrentMLSCiphersuite,
	).Scan(&n)
	return n, err
}

// PurgeExpiredMLSKeyPackages deletes key package rows that are safe to remove:
//   - Consumed rows older than 30 days (no longer needed for audit or retry).
//   - Unconsumed rows whose expiry has passed (useless - would be rejected on consume).
//
// Last-resort packages are never deleted. Processes at most 10 000 rows per call
// to bound the DELETE scan time (consistent with the prior OPK cleanup pattern).
// Returns the number of rows deleted.
func (p *Pool) PurgeExpiredMLSKeyPackages(ctx context.Context) (int64, error) {
	tag, err := p.Exec(ctx, `
		DELETE FROM mls_key_packages
		WHERE id IN (
			SELECT id FROM mls_key_packages
			WHERE (
				(consumed_at IS NOT NULL AND created_at < now() - interval '30 days')
				OR
				(consumed_at IS NULL AND expires_at < now() AND last_resort = false)
			)
			LIMIT 10000
		)`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ListDeviceIDsForUser returns all device IDs that have an MLS credential for the user.
func (p *Pool) ListDeviceIDsForUser(ctx context.Context, userID string) ([]string, error) {
	rows, err := p.Query(ctx, `
		SELECT device_id FROM mls_credentials
		WHERE user_id = $1
		ORDER BY device_id`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		ids = append(ids, d)
	}
	return ids, rows.Err()
}

// UpsertDevice inserts or updates a device for a user (e.g. on credential upload).
func (p *Pool) UpsertDevice(ctx context.Context, userID, deviceID, label string) error {
	_, err := p.Exec(ctx, `
		INSERT INTO devices (user_id, device_id, label, last_seen)
		VALUES ($1, $2, NULLIF($3, ''), now())
		ON CONFLICT (user_id, device_id) DO UPDATE SET last_seen = now()`,
		userID, deviceID, label,
	)
	return err
}
