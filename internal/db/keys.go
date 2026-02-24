package db

import (
	"context"
	"errors"

	"hush.app/server/internal/models"

	"github.com/jackc/pgx/v5"
)

// UpsertIdentityKeys inserts or replaces identity keys for a user+device.
func (p *Pool) UpsertIdentityKeys(ctx context.Context, userID, deviceID string, identityKey, signedPreKey, signedPreKeySignature []byte, registrationID int) error {
	_, err := p.Exec(ctx, `
		INSERT INTO signal_identity_keys (user_id, device_id, identity_key, signed_pre_key, signed_pre_key_signature, registration_id)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, device_id) DO UPDATE SET
			identity_key = EXCLUDED.identity_key,
			signed_pre_key = EXCLUDED.signed_pre_key,
			signed_pre_key_signature = EXCLUDED.signed_pre_key_signature,
			registration_id = EXCLUDED.registration_id`,
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
