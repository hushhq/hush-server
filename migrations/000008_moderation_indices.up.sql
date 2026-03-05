-- Moderation performance indices for guild-scoped queries.
-- Migration 000007 added server_id columns to bans, mutes, and audit_log but omitted indices.
-- Without these indices, queries on server_id degrade to sequential scans as data grows.

-- Partial index for active ban lookups by guild.
CREATE INDEX IF NOT EXISTS idx_bans_server_id
    ON bans(server_id)
    WHERE lifted_at IS NULL;

-- Composite index for GetActiveBan and invite-claim ban check (guild + user, active only).
CREATE INDEX IF NOT EXISTS idx_bans_server_user
    ON bans(server_id, user_id)
    WHERE lifted_at IS NULL;

-- Partial index for active mute lookups by guild.
CREATE INDEX IF NOT EXISTS idx_mutes_server_id
    ON mutes(server_id)
    WHERE lifted_at IS NULL;

-- Composite index for GetActiveMute (guild + user, active only).
CREATE INDEX IF NOT EXISTS idx_mutes_server_user
    ON mutes(server_id, user_id)
    WHERE lifted_at IS NULL;

-- Index for guild-scoped audit log queries.
CREATE INDEX IF NOT EXISTS idx_audit_log_server_id
    ON audit_log(server_id);

-- Composite index for paginated guild audit log (ordered by created_at DESC).
CREATE INDEX IF NOT EXISTS idx_audit_log_server_created
    ON audit_log(server_id, created_at DESC);
