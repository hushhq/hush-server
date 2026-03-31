-- Multi-tenant restoration: re-create servers and server_members, re-add server_id FKs.
-- This is a forward migration on top of 000005 (single-tenant) and 000006 (moderation).
-- All new FK columns are NULLABLE so existing rows are unaffected.

-- 1. Create servers table
CREATE TABLE servers (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL,
    icon_url   TEXT,
    owner_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 2. Create server_members table
CREATE TABLE server_members (
    server_id  UUID        NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role       TEXT        NOT NULL DEFAULT 'member'
        CHECK (role IN ('owner', 'admin', 'mod', 'member')),
    joined_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, user_id)
);

CREATE INDEX idx_server_members_server_id ON server_members(server_id);
CREATE INDEX idx_server_members_user_id   ON server_members(user_id);

-- 3. Add server_id FK to channels (NULLABLE - existing rows have no guild)
ALTER TABLE channels
    ADD COLUMN server_id UUID REFERENCES servers(id) ON DELETE CASCADE;

CREATE INDEX idx_channels_server_id ON channels(server_id);

-- 4. Add server_id FK to invite_codes (NULLABLE)
ALTER TABLE invite_codes
    ADD COLUMN server_id UUID REFERENCES servers(id) ON DELETE CASCADE;

-- 5. Add server_creation_policy to instance_config
ALTER TABLE instance_config
    ADD COLUMN server_creation_policy TEXT NOT NULL DEFAULT 'any_member'
        CHECK (server_creation_policy IN ('any_member', 'admin_only', 'paid_only'));

-- 6. Add server_id FK to bans (NULLABLE)
ALTER TABLE bans
    ADD COLUMN server_id UUID REFERENCES servers(id) ON DELETE CASCADE;

-- 7. Add server_id FK to mutes (NULLABLE)
ALTER TABLE mutes
    ADD COLUMN server_id UUID REFERENCES servers(id) ON DELETE CASCADE;

-- 8. Add server_id FK to audit_log (NULLABLE)
ALTER TABLE audit_log
    ADD COLUMN server_id UUID REFERENCES servers(id) ON DELETE CASCADE;
