package db

import (
	"context"
	"errors"
	"time"

	"hush.app/server/internal/models"

	"github.com/jackc/pgx/v5"
)

// UpsertIdentityKeys inserts or replaces identity keys for a user+device.
// On conflict it refreshes spk_uploaded_at so staleness checks reflect the latest upload.
// spk_key_id is NOT modified here — it stays at the row's current value (RotateSPK
// handles explicit key-ID increments during rotation).
func (p *Pool) UpsertIdentityKeys(ctx context.Context, userID, deviceID string, identityKey, signedPreKey, signedPreKeySignature []byte, registrationID int) error {
	_, err := p.Exec(ctx, `
		INSERT INTO signal_identity_keys
			(user_id, device_id, identity_key, signed_pre_key, signed_pre_key_signature, registration_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, device_id) DO UPDATE SET
			identity_key             = EXCLUDED.identity_key,
			signed_pre_key           = EXCLUDED.signed_pre_key,
			signed_pre_key_signature = EXCLUDED.signed_pre_key_signature,
			registration_id          = EXCLUDED.registration_id,
			spk_uploaded_at          = now()`,
		userID, deviceID, identityKey, signedPreKey, signedPreKeySignature, registrationID,
	)
	return err
}

// InsertOneTimePreKeys inserts a batch of one-time pre-keys for a user+device.
func (p *Pool) InsertOneTimePreKeys(ctx context.Context, userID, deviceID string, keys []models.OneTimePreKeyRow) error {
	if len(keys) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, k := range keys {
		batch.Queue(`
			INSERT INTO signal_one_time_pre_keys (user_id, device_id, key_id, public_key, used)
			VALUES ($1, $2, $3, $4, false)`,
			userID, deviceID, k.KeyID, k.PublicKey,
		)
	}
	br := p.SendBatch(ctx, batch)
	defer br.Close()
	for range keys {
		_, err := br.Exec()
		if err != nil {
			return err
		}
	}
	return nil
}

// GetIdentityAndSignedPreKey returns identity and signed pre-key for a user+device.
// Callers that do not need the SPK key ID or upload timestamp use this method.
func (p *Pool) GetIdentityAndSignedPreKey(ctx context.Context, userID, deviceID string) (identityKey, signedPreKey, signedPreKeySignature []byte, registrationID int, err error) {
	err = p.QueryRow(ctx, `
		SELECT identity_key, signed_pre_key, signed_pre_key_signature, registration_id
		FROM signal_identity_keys
		WHERE user_id = $1 AND device_id = $2`,
		userID, deviceID,
	).Scan(&identityKey, &signedPreKey, &signedPreKeySignature, &registrationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, 0, nil
		}
		return nil, nil, nil, 0, err
	}
	return identityKey, signedPreKey, signedPreKeySignature, registrationID, nil
}

// GetIdentityAndSignedPreKeyWithID returns identity keys, the current SPK, its key ID,
// and the time it was last uploaded. Used by buildBundle to include spk_key_id in the
// response and to determine whether to emit a keys.spk_stale warning.
func (p *Pool) GetIdentityAndSignedPreKeyWithID(ctx context.Context, userID, deviceID string) (
	identityKey, signedPreKey, signedPreKeySignature []byte,
	registrationID, spkKeyID int,
	spkUploadedAt time.Time,
	err error,
) {
	err = p.QueryRow(ctx, `
		SELECT identity_key, signed_pre_key, signed_pre_key_signature,
		       registration_id, spk_key_id, spk_uploaded_at
		FROM signal_identity_keys
		WHERE user_id = $1 AND device_id = $2`,
		userID, deviceID,
	).Scan(&identityKey, &signedPreKey, &signedPreKeySignature,
		&registrationID, &spkKeyID, &spkUploadedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, nil, 0, 0, time.Time{}, nil
		}
		return nil, nil, nil, 0, 0, time.Time{}, err
	}
	return identityKey, signedPreKey, signedPreKeySignature, registrationID, spkKeyID, spkUploadedAt, nil
}

// RotateSPK archives the current SPK into signal_spk_history (retaining private key for
// the 48h grace period) and atomically replaces the live SPK in signal_identity_keys.
// All writes happen inside a single transaction.
func (p *Pool) RotateSPK(
	ctx context.Context,
	userID, deviceID string,
	newSPKKeyID int,
	newSPKPublic, newSPKSig []byte,
	oldSPKKeyID int,
	oldSPKPublic, oldSPKSig, oldSPKPrivate []byte,
) error {
	tx, err := p.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Archive old SPK with private key so in-transit sessions can still decrypt.
	_, err = tx.Exec(ctx, `
		INSERT INTO signal_spk_history
			(user_id, device_id, spk_key_id, public_key, private_key, signature, superseded_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (user_id, device_id, spk_key_id) DO UPDATE SET
			private_key   = EXCLUDED.private_key,
			superseded_at = now()`,
		userID, deviceID, oldSPKKeyID, oldSPKPublic, oldSPKPrivate, oldSPKSig,
	)
	if err != nil {
		return err
	}

	// Promote new SPK as the live key.
	_, err = tx.Exec(ctx, `
		UPDATE signal_identity_keys
		SET signed_pre_key           = $3,
		    signed_pre_key_signature = $4,
		    spk_key_id               = $5,
		    spk_uploaded_at          = now()
		WHERE user_id = $1 AND device_id = $2`,
		userID, deviceID, newSPKPublic, newSPKSig, newSPKKeyID,
	)
	if err != nil {
		return err
	}

	return tx.Commit(ctx)
}

// GetHistoricalSPK returns a superseded SPK from signal_spk_history by key ID,
// but only while its private key has not yet been NULLed (i.e. within the 48h grace
// period). Returns (nil, nil, nil, nil) when no matching row exists or the grace period
// has expired.
func (p *Pool) GetHistoricalSPK(ctx context.Context, userID, deviceID string, spkKeyID int) (
	publicKey, privateKey, signature []byte, err error,
) {
	err = p.QueryRow(ctx, `
		SELECT public_key, private_key, signature
		FROM signal_spk_history
		WHERE user_id = $1 AND device_id = $2 AND spk_key_id = $3
		  AND private_key IS NOT NULL`,
		userID, deviceID, spkKeyID,
	).Scan(&publicKey, &privateKey, &signature)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil, nil
	}
	return publicKey, privateKey, signature, err
}

// PurgeExpiredSPKPrivateKeys NULLs out private keys in signal_spk_history whose 48h
// grace period has elapsed. Returns the number of rows updated.
func (p *Pool) PurgeExpiredSPKPrivateKeys(ctx context.Context) (int64, error) {
	tag, err := p.Exec(ctx, `
		UPDATE signal_spk_history
		SET private_key = NULL
		WHERE private_key IS NOT NULL
		  AND superseded_at < now() - interval '48 hours'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// PurgeConsumedOneTimePreKeys deletes used OPK rows older than olderThanDays days.
// Processes at most 10 000 rows per call to bound the DELETE scan time.
// Returns the number of rows deleted.
func (p *Pool) PurgeConsumedOneTimePreKeys(ctx context.Context, olderThanDays int) (int64, error) {
	tag, err := p.Exec(ctx, `
		DELETE FROM signal_one_time_pre_keys
		WHERE id IN (
			SELECT id FROM signal_one_time_pre_keys
			WHERE used = true
			  AND created_at < now() - ($1 * interval '1 day')
			LIMIT 10000
		)`,
		olderThanDays,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ConsumeOneTimePreKey atomically marks one unused one-time pre-key as used and returns it.
func (p *Pool) ConsumeOneTimePreKey(ctx context.Context, userID, deviceID string) (keyID int, publicKey []byte, err error) {
	err = p.QueryRow(ctx, `
		UPDATE signal_one_time_pre_keys
		SET used = true
		WHERE id = (
			SELECT id FROM signal_one_time_pre_keys
			WHERE user_id = $1 AND device_id = $2 AND used = false
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING key_id, public_key`,
		userID, deviceID,
	).Scan(&keyID, &publicKey)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, nil
		}
		return 0, nil, err
	}
	return keyID, publicKey, nil
}

// CountUnusedOneTimePreKeys returns the number of unused one-time pre-keys for a user+device.
func (p *Pool) CountUnusedOneTimePreKeys(ctx context.Context, userID, deviceID string) (int, error) {
	var n int
	err := p.QueryRow(ctx, `
		SELECT COUNT(*) FROM signal_one_time_pre_keys
		WHERE user_id = $1 AND device_id = $2 AND used = false`,
		userID, deviceID,
	).Scan(&n)
	return n, err
}

// ListDeviceIDsForUser returns all device IDs that have identity keys for the user.
func (p *Pool) ListDeviceIDsForUser(ctx context.Context, userID string) ([]string, error) {
	rows, err := p.Query(ctx, `
		SELECT device_id FROM signal_identity_keys
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

// UpsertDevice inserts or updates a device for a user (e.g. on key upload).
func (p *Pool) UpsertDevice(ctx context.Context, userID, deviceID, label string) error {
	_, err := p.Exec(ctx, `
		INSERT INTO devices (user_id, device_id, label, last_seen)
		VALUES ($1, $2, NULLIF($3, ''), now())
		ON CONFLICT (user_id, device_id) DO UPDATE SET last_seen = now()`,
		userID, deviceID, label,
	)
	return err
}
