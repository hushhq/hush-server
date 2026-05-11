-- Reverse of migration 000036: strip the ciphersuite column from MLS state
-- tables and restore the original index/uniqueness shapes.

-- 4. mls_pending_welcomes
DROP INDEX IF EXISTS idx_mls_pending_welcomes_recipient;
ALTER TABLE mls_pending_welcomes DROP COLUMN IF EXISTS ciphersuite;
CREATE INDEX IF NOT EXISTS idx_mls_pending_welcomes_recipient
    ON mls_pending_welcomes (recipient_user_id);

-- 3. mls_commits
DROP INDEX IF EXISTS idx_mls_commits_channel_epoch;
ALTER TABLE mls_commits DROP COLUMN IF EXISTS ciphersuite;
CREATE INDEX IF NOT EXISTS idx_mls_commits_channel_epoch
    ON mls_commits (channel_id, epoch);

-- 2. mls_group_info
DROP INDEX IF EXISTS mls_group_info_channel_type;
DROP INDEX IF EXISTS mls_group_info_server_type;
ALTER TABLE mls_group_info DROP COLUMN IF EXISTS ciphersuite;
CREATE UNIQUE INDEX IF NOT EXISTS mls_group_info_channel_type
    ON mls_group_info (channel_id, group_type)
    WHERE channel_id IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS mls_group_info_server_type
    ON mls_group_info (server_id, group_type)
    WHERE server_id IS NOT NULL;

-- 1. mls_key_packages
DROP INDEX IF EXISTS idx_mls_kp_user_device_unused;
DROP INDEX IF EXISTS idx_mls_kp_count;
ALTER TABLE mls_key_packages DROP COLUMN IF EXISTS ciphersuite;
CREATE INDEX IF NOT EXISTS idx_mls_kp_user_device_unused
    ON mls_key_packages (user_id, device_id)
    WHERE consumed_at IS NULL AND last_resort = false;
CREATE INDEX IF NOT EXISTS idx_mls_kp_count
    ON mls_key_packages (user_id, device_id, consumed_at);
