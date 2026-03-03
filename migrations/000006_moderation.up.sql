CREATE TABLE bans (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    actor_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason      TEXT        NOT NULL,
    expires_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    lifted_at   TIMESTAMPTZ,
    lifted_by   UUID        REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_bans_active ON bans(user_id) WHERE lifted_at IS NULL;

CREATE TABLE mutes (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    actor_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason      TEXT        NOT NULL,
    expires_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    lifted_at   TIMESTAMPTZ,
    lifted_by   UUID        REFERENCES users(id) ON DELETE SET NULL
);

CREATE INDEX idx_mutes_active ON mutes(user_id) WHERE lifted_at IS NULL;

CREATE TABLE audit_log (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_id   UUID        REFERENCES users(id) ON DELETE SET NULL,
    action      TEXT        NOT NULL,
    reason      TEXT        NOT NULL,
    metadata    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_audit_log_created_at ON audit_log(created_at DESC);
