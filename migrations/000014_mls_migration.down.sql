-- Migration 000014 reverse: drop MLS tables, recreate Signal tables.

DROP TABLE IF EXISTS mls_key_packages CASCADE;
DROP TABLE IF EXISTS mls_credentials CASCADE;

-- Restore signal_identity_keys (from 000001_init_schema.up.sql + 000013_spk_lifecycle.up.sql).
CREATE TABLE signal_identity_keys (
    user_id                  UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id                TEXT        NOT NULL,
    identity_key             BYTEA       NOT NULL,
    signed_pre_key           BYTEA       NOT NULL,
    signed_pre_key_signature BYTEA       NOT NULL,
    registration_id          INT         NOT NULL,
    spk_key_id               INT         NOT NULL DEFAULT 1,
    spk_uploaded_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, device_id)
);

-- Restore signal_one_time_pre_keys (from 000001 + 000013).
CREATE TABLE signal_one_time_pre_keys (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id  TEXT        NOT NULL,
    key_id     INT         NOT NULL,
    public_key BYTEA       NOT NULL,
    used       BOOLEAN     NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_signal_otpk_user_device ON signal_one_time_pre_keys(user_id, device_id, used);

-- Restore signal_spk_history (from 000013_spk_lifecycle.up.sql).
CREATE TABLE signal_spk_history (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id     TEXT        NOT NULL,
    spk_key_id    INT         NOT NULL,
    public_key    BYTEA       NOT NULL,
    private_key   BYTEA,
    signature     BYTEA       NOT NULL,
    uploaded_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    superseded_at TIMESTAMPTZ,
    UNIQUE (user_id, device_id, spk_key_id)
);

CREATE INDEX idx_spk_history_user_device ON signal_spk_history(user_id, device_id);
CREATE INDEX idx_spk_history_grace ON signal_spk_history(superseded_at)
    WHERE superseded_at IS NOT NULL AND private_key IS NOT NULL;
