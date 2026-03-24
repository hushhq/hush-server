package db

import (
	"context"
	"time"
)

// InsertAuthNonce stores a challenge nonce associated with a user's public key.
// The nonce expires at expiresAt; expired nonces are rejected by ConsumeAuthNonce.
func (p *Pool) InsertAuthNonce(ctx context.Context, nonce string, publicKey []byte, expiresAt time.Time) error {
	_, err := p.Exec(ctx, `
		INSERT INTO auth_nonces (nonce, user_public_key, expires_at)
		VALUES ($1, $2, $3)`,
		nonce, publicKey, expiresAt,
	)
	return err
}

// ConsumeAuthNonce atomically deletes and returns the public key stored with
// nonce if and only if the nonce exists and has not yet expired. Returns
// pgx.ErrNoRows (which satisfies errors.Is(err, sql.ErrNoRows)) when the
// nonce is absent or expired.
func (p *Pool) ConsumeAuthNonce(ctx context.Context, nonce string) ([]byte, error) {
	row := p.QueryRow(ctx, `
		DELETE FROM auth_nonces
		WHERE nonce = $1 AND expires_at > now()
		RETURNING user_public_key`,
		nonce,
	)
	var publicKey []byte
	if err := row.Scan(&publicKey); err != nil {
		return nil, err
	}
	return publicKey, nil
}

// PurgeExpiredNonces deletes all auth_nonces whose expires_at is in the past.
// Returns the number of rows deleted.
func (p *Pool) PurgeExpiredNonces(ctx context.Context) (int64, error) {
	tag, err := p.Exec(ctx, `DELETE FROM auth_nonces WHERE expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
