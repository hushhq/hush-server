-- Migration 000014: MLS migration — drop Signal tables, create MLS credential and key package tables.

-- Drop Signal tables (CASCADE removes all FKs and indices).
DROP TABLE IF EXISTS signal_spk_history CASCADE;
DROP TABLE IF EXISTS signal_one_time_pre_keys CASCADE;
DROP TABLE IF EXISTS signal_identity_keys CASCADE;

-- MLS credential: one row per user+device, stores signing keypair binding.
-- identity_version: 1=UUID-based (M.1), 2=pubkey-based (Phase J).
CREATE TABLE mls_credentials (
    user_id          UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id        TEXT        NOT NULL,
    credential_bytes BYTEA       NOT NULL,
    signing_pub_key  BYTEA       NOT NULL,
    identity_version INT         NOT NULL DEFAULT 1,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, device_id)
);

-- MLS key packages: many per user+device, consumed atomically.
-- last_resort packages are never auto-deleted and reused when no regular packages remain.
CREATE TABLE mls_key_packages (
    id                  UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id             UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id           TEXT        NOT NULL,
    key_package_bytes   BYTEA       NOT NULL,
    last_resort         BOOLEAN     NOT NULL DEFAULT false,
    expires_at          TIMESTAMPTZ NOT NULL,
    consumed_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Index for the hot path: fetch one unused non-last-resort package for a device.
CREATE INDEX idx_mls_kp_user_device_unused
    ON mls_key_packages(user_id, device_id)
    WHERE consumed_at IS NULL AND last_resort = false;

-- Index for count queries and replenishment check.
CREATE INDEX idx_mls_kp_count
    ON mls_key_packages(user_id, device_id, consumed_at);

-- Index for expiration cleanup job.
CREATE INDEX idx_mls_kp_expires
    ON mls_key_packages(expires_at)
    WHERE last_resort = false;
