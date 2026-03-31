-- Migration 000018: BIP39 cryptographic identity
-- Replaces password-based auth with public-key challenge-response (Ed25519).
-- Adds auth_nonces (challenge tokens) and device_keys (certified device public keys).

-- ============================================================
-- 1. users table - replace password_hash with root_public_key
-- ============================================================

-- Remove password authentication column.
ALTER TABLE users DROP COLUMN IF EXISTS password_hash;

-- Add root Ed25519 public key (32 bytes). A temporary default permits the
-- migration to run against existing rows; the default is then removed so all
-- future inserts must supply a real public key.
ALTER TABLE users ADD COLUMN root_public_key BYTEA NOT NULL DEFAULT '\x00'::BYTEA;

-- Assign unique placeholder keys to pre-existing users so the UNIQUE constraint
-- can be created. These accounts predate BIP39 and must re-register.
-- gen_random_bytes(32) produces unique 32-byte random values per row.
UPDATE users SET root_public_key = gen_random_bytes(32) WHERE root_public_key = '\x00'::BYTEA;

-- Enforce global uniqueness of root public keys.
ALTER TABLE users ADD CONSTRAINT users_root_public_key_unique UNIQUE (root_public_key);

-- Remove the placeholder default so future INSERTs must provide the real key.
ALTER TABLE users ALTER COLUMN root_public_key DROP DEFAULT;

-- ============================================================
-- 2. auth_nonces table - short-lived challenge tokens
-- ============================================================

CREATE TABLE auth_nonces (
    nonce           TEXT        PRIMARY KEY,
    user_public_key BYTEA       NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL
);

-- Supports efficient purge of expired nonces.
CREATE INDEX idx_auth_nonces_expires_at ON auth_nonces(expires_at);

-- ============================================================
-- 3. device_keys table - certified per-device public keys
-- ============================================================

CREATE TABLE device_keys (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id          UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id        TEXT        NOT NULL,
    device_public_key BYTEA      NOT NULL,
    -- Certificate is the root-key signature over device_public_key; NULL for
    -- the first device, which is created at registration time.
    certificate      BYTEA,
    certified_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen        TIMESTAMPTZ,
    label            TEXT,
    UNIQUE (user_id, device_id)
);

-- Supports fast lookup of all devices belonging to a user.
CREATE INDEX idx_device_keys_user_id ON device_keys(user_id);
