-- Migration 000037: align linked-device approval keys with root-key auth.
--
-- Device linking transfers the account root private key to the new device. The
-- device-link request also carries an ephemeral public key, but that key is only
-- a pairing commitment used during link approval. Future approvals from the
-- linked device are signed by the root private key, so every local device row
-- for a user must verify against users.root_public_key.

UPDATE device_keys AS dk
SET device_public_key = u.root_public_key
FROM users AS u
WHERE dk.user_id = u.id
  AND dk.device_public_key IS DISTINCT FROM u.root_public_key;
