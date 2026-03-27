-- 000021_dm_and_discovery.up.sql
-- Adds DM guild support and guild discoverability columns.

-- DM guild flag: true for 2-person direct message guilds.
ALTER TABLE servers
    ADD COLUMN is_dm BOOLEAN NOT NULL DEFAULT false;

-- Category for discoverability filtering (gaming, music, etc.).
ALTER TABLE servers
    ADD COLUMN category TEXT
        CHECK (category IN (
            'gaming', 'technology', 'music', 'art', 'education',
            'science', 'community', 'sports', 'entertainment', 'other'
        ));

-- Enforce that discoverable guilds must declare a category.
-- Guild admins opt in to plaintext exposure by enabling discoverability.
ALTER TABLE servers
    ADD CONSTRAINT servers_discoverable_category_check
        CHECK (NOT discoverable OR category IS NOT NULL);

-- Public plaintext name for the /explore page card (discoverable guilds only).
-- This is a deliberate opacity tradeoff: guild admins opt in when enabling discoverability.
ALTER TABLE servers
    ADD COLUMN public_name TEXT;

-- Public plaintext description for the /explore page card.
ALTER TABLE servers
    ADD COLUMN public_description TEXT;

-- dm_pairs ensures each (user_a, user_b) pair maps to exactly one DM guild.
-- user_a_id < user_b_id canonical ordering makes the UNIQUE constraint
-- catch duplicates regardless of call-site parameter order.
CREATE TABLE IF NOT EXISTS dm_pairs (
    server_id   UUID        NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    user_a_id   TEXT        NOT NULL,
    user_b_id   TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id),
    CONSTRAINT dm_pairs_user_order CHECK (user_a_id < user_b_id),
    UNIQUE (user_a_id, user_b_id)
);

-- Index for fast DM lookup by either participant.
CREATE INDEX IF NOT EXISTS dm_pairs_user_a_idx ON dm_pairs (user_a_id);
CREATE INDEX IF NOT EXISTS dm_pairs_user_b_idx ON dm_pairs (user_b_id);

-- Index for discover page queries (discoverable + category + member_count sort).
CREATE INDEX IF NOT EXISTS servers_discover_idx
    ON servers (discoverable, access_policy, member_count DESC)
    WHERE discoverable = true;
