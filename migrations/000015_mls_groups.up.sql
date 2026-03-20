-- MLS group info: one row per channel, stores current GroupInfo for External Commit joins
CREATE TABLE IF NOT EXISTS mls_group_info (
    channel_id       UUID PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
    group_info_bytes BYTEA NOT NULL,
    epoch            BIGINT NOT NULL DEFAULT 0,
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- MLS commit queue: stores all Commits per channel for offline recovery
CREATE TABLE IF NOT EXISTS mls_commits (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id   UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    epoch        BIGINT NOT NULL,
    commit_bytes BYTEA NOT NULL,
    sender_id    UUID NOT NULL REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mls_commits_channel_epoch
    ON mls_commits (channel_id, epoch);

-- MLS pending welcomes: stores Welcome messages for offline members
CREATE TABLE IF NOT EXISTS mls_pending_welcomes (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id        UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    recipient_user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    welcome_bytes     BYTEA NOT NULL,
    sender_id         UUID NOT NULL REFERENCES users(id),
    epoch             BIGINT NOT NULL DEFAULT 0,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_mls_pending_welcomes_recipient
    ON mls_pending_welcomes (recipient_user_id);
