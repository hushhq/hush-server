-- Migration 000013: SPK lifecycle - version history, staleness tracking, OPK created_at

-- Track current SPK version and upload time on each device's identity key row.
ALTER TABLE signal_identity_keys
    ADD COLUMN spk_key_id    INT         NOT NULL DEFAULT 1,
    ADD COLUMN spk_uploaded_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Track consumed-at time on one-time pre-keys so the cleanup job can prune old rows.
ALTER TABLE signal_one_time_pre_keys
    ADD COLUMN created_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- Historical SPKs: private key retained for 48h grace period, then NULLed.
-- Metadata (public key, signature, timestamps) kept indefinitely for audit.
CREATE TABLE signal_spk_history (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id     TEXT        NOT NULL,
    spk_key_id    INT         NOT NULL,
    public_key    BYTEA       NOT NULL,
    private_key   BYTEA,                          -- NULL after 48h grace period
    signature     BYTEA       NOT NULL,
    uploaded_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    superseded_at TIMESTAMPTZ,                    -- set when a newer SPK replaces this one
    UNIQUE (user_id, device_id, spk_key_id)
);

-- Efficient lookup of a device's full SPK history (grace period lookup, audit).
CREATE INDEX idx_spk_history_user_device ON signal_spk_history(user_id, device_id);

-- Partial index covering only rows still in the grace period (private_key IS NOT NULL
-- AND superseded_at IS NOT NULL). The cleanup job uses this to find rows to NULL out.
CREATE INDEX idx_spk_history_grace ON signal_spk_history(superseded_at)
    WHERE superseded_at IS NOT NULL AND private_key IS NOT NULL;
