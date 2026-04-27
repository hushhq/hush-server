package db

import (
	"context"

	"github.com/hushhq/hush-server/internal/models"
)

// BackfillRootDeviceKey inserts a device key row only when no row exists
// for (userID, deviceID). Used by the post-/verify backfill path so a
// legitimate root-key holder cannot rewrite an already-certified device's
// public key by re-presenting /verify with the same deviceID.
//
// Returns (true, nil) when a row was inserted. Returns (false, nil) when
// a row already existed (and was left untouched). Returns (false, err)
// only on hard DB failures.
func (p *Pool) BackfillRootDeviceKey(ctx context.Context, userID, deviceID string, devicePublicKey []byte) (bool, error) {
	tag, err := p.Exec(ctx, `
		INSERT INTO device_keys (user_id, device_id, device_public_key, certificate, label)
		VALUES ($1, $2, $3, NULL, NULL)
		ON CONFLICT (user_id, device_id) DO NOTHING`,
		userID, deviceID, devicePublicKey,
	)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// InsertDeviceKey stores a certified device public key for a user.
// certificate may be nil for the first (root) device registered at sign-up.
// On conflict (user_id, device_id) the existing row is overwritten so that
// device re-registration (e.g. after a factory reset) is handled gracefully.
func (p *Pool) InsertDeviceKey(ctx context.Context, userID, deviceID, label string, devicePublicKey, certificate []byte) error {
	_, err := p.Exec(ctx, `
		INSERT INTO device_keys (user_id, device_id, device_public_key, certificate, label)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))
		ON CONFLICT (user_id, device_id) DO UPDATE
		    SET device_public_key = EXCLUDED.device_public_key,
		        certificate       = EXCLUDED.certificate,
		        certified_at      = now(),
		        label             = COALESCE(NULLIF(EXCLUDED.label, ''), device_keys.label)`,
		userID, deviceID, devicePublicKey, certificate, label,
	)
	return err
}

// ListDeviceKeys returns all device keys belonging to a user, ordered by
// certified_at ascending so the first (root) device appears first.
func (p *Pool) ListDeviceKeys(ctx context.Context, userID string) ([]models.DeviceKey, error) {
	rows, err := p.Query(ctx, `
		SELECT id, user_id, device_id, device_public_key, certificate, certified_at, last_seen, label
		FROM device_keys
		WHERE user_id = $1
		ORDER BY certified_at ASC`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []models.DeviceKey
	for rows.Next() {
		var dk models.DeviceKey
		if err := rows.Scan(
			&dk.ID, &dk.UserID, &dk.DeviceID,
			&dk.DevicePublicKey, &dk.Certificate,
			&dk.CertifiedAt, &dk.LastSeen, &dk.Label,
		); err != nil {
			return nil, err
		}
		keys = append(keys, dk)
	}
	return keys, rows.Err()
}

// IsDeviceActive reports whether a device key row still exists for the
// (userID, deviceID) pair. Used by RequireAuth and the WS upgrade path
// to enforce revocation: a device whose key has been deleted by
// RevokeDeviceKey loses its ability to make authenticated calls on
// the next request.
func (p *Pool) IsDeviceActive(ctx context.Context, userID, deviceID string) (bool, error) {
	var exists bool
	err := p.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM device_keys
			WHERE user_id = $1 AND device_id = $2
		)`,
		userID, deviceID,
	).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// RevokeDeviceKey deletes the device key for (userID, deviceID). No-op if not found.
func (p *Pool) RevokeDeviceKey(ctx context.Context, userID, deviceID string) error {
	_, err := p.Exec(ctx, `
		DELETE FROM device_keys WHERE user_id = $1 AND device_id = $2`,
		userID, deviceID,
	)
	return err
}

// RevokeAllDeviceKeys deletes every device key for a user (used on account wipe).
func (p *Pool) RevokeAllDeviceKeys(ctx context.Context, userID string) error {
	_, err := p.Exec(ctx, `DELETE FROM device_keys WHERE user_id = $1`, userID)
	return err
}

// UpdateDeviceLastSeen sets last_seen = now() for the given device.
// No-op if the device does not exist.
func (p *Pool) UpdateDeviceLastSeen(ctx context.Context, userID, deviceID string) error {
	_, err := p.Exec(ctx, `
		UPDATE device_keys SET last_seen = now()
		WHERE user_id = $1 AND device_id = $2`,
		userID, deviceID,
	)
	return err
}
