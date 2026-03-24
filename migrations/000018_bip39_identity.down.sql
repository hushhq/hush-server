-- Migration 000018 rollback: restore password-based auth schema.

DROP TABLE IF EXISTS device_keys;
DROP TABLE IF EXISTS auth_nonces;

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_root_public_key_unique;
ALTER TABLE users DROP COLUMN IF EXISTS root_public_key;
ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash TEXT;
