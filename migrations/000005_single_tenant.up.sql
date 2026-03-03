-- Single-tenant migration: drop multi-server model, add instance config and user roles.
-- Order matters: FK dependencies resolved bottom-up.

-- 1. Add role column to users
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'member'
    CHECK (role IN ('owner', 'admin', 'mod', 'member'));

-- 2. Create single-row instance config table
CREATE TABLE instance_config (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name              TEXT        NOT NULL DEFAULT 'Hush',
    icon_url          TEXT,
    owner_id          UUID        REFERENCES users(id) ON DELETE SET NULL,
    registration_mode TEXT        NOT NULL DEFAULT 'invite_only'
        CHECK (registration_mode IN ('open', 'invite_only')),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 3. Seed the single instance config row
INSERT INTO instance_config DEFAULT VALUES;

-- 4. Drop server_id from invite_codes
ALTER TABLE invite_codes DROP COLUMN server_id;

-- 5. Drop server_id from channels
ALTER TABLE channels DROP COLUMN server_id;

-- 6. Drop server_members (must drop before servers due to FK)
DROP TABLE server_members;

-- 7. Drop servers
DROP TABLE servers;
