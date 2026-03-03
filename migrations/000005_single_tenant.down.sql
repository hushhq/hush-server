-- Rollback single-tenant migration: restore multi-server schema.
-- Order is reverse of up migration.

-- 1. Restore servers table
CREATE TABLE servers (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        NOT NULL,
    icon_url   TEXT,
    owner_id   UUID        NOT NULL REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 2. Restore server_members table
CREATE TABLE server_members (
    id        UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    server_id UUID        NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    user_id   UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role      TEXT        NOT NULL DEFAULT 'member',
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(server_id, user_id)
);

-- 3. Restore server_id on channels
ALTER TABLE channels ADD COLUMN server_id UUID REFERENCES servers(id) ON DELETE CASCADE;

-- 4. Restore server_id on invite_codes
ALTER TABLE invite_codes ADD COLUMN server_id UUID REFERENCES servers(id) ON DELETE CASCADE;

-- 5. Remove instance config seed row
DELETE FROM instance_config;

-- 6. Drop instance_config table
DROP TABLE instance_config;

-- 7. Remove role column from users
ALTER TABLE users DROP COLUMN role;
