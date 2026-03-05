-- Instance-level bans (IROLE-03)
CREATE TABLE IF NOT EXISTS instance_bans (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    actor_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reason      TEXT        NOT NULL,
    expires_at  TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    lifted_at   TIMESTAMPTZ,
    lifted_by   UUID        REFERENCES users(id) ON DELETE SET NULL
);

-- Partial index for active ban lookups (mirrors guild bans pattern)
CREATE INDEX IF NOT EXISTS idx_instance_bans_active ON instance_bans(user_id) WHERE lifted_at IS NULL;

-- Instance-level audit log (IROLE-05)
CREATE TABLE IF NOT EXISTS instance_audit_log (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_id   UUID        REFERENCES users(id) ON DELETE SET NULL,
    action      TEXT        NOT NULL,
    reason      TEXT        NOT NULL,
    metadata    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_instance_audit_log_created ON instance_audit_log(created_at DESC);
