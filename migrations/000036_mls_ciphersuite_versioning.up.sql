-- Migration 000036: MLS ciphersuite versioning for X-Wing rollout.
--
-- Background
-- ----------
-- The delivery service previously stored MLS KeyPackages, GroupInfo, Commits, and
-- pending Welcomes as opaque BYTEA blobs with no ciphersuite metadata. After the
-- post-quantum migration to MLS_256_XWING_CHACHA20POLY1305_SHA256_Ed25519
-- (codepoint 0x004D, decimal 77) it is no longer safe to surface or reuse rows
-- that belonged to the previous ciphersuite (MLS_128_DHKEMX25519_AES128GCM_SHA256_Ed25519,
-- codepoint 0x0001, decimal 1): clients on the new suite cannot consume them and
-- silently mixing epochs across suites breaks OpenMLS protocol-level checks.
--
-- This migration:
--   1. Adds a ciphersuite column to every blob-bearing MLS state table.
--   2. Backfills every existing row to 1 (legacy ciphersuite) so the historical
--      state is correctly labeled, not silently mislabeled as X-Wing.
--   3. Marks the column NOT NULL with no DEFAULT, forcing every writer to stamp
--      an explicit suite (the server constant CurrentMLSCiphersuite).
--   4. Adjusts mls_group_info uniqueness so an old-suite row does not block a
--      fresh current-suite row from being created for the same channel/server.
--   5. Updates lookup indices so current-suite reads stay efficient.
--
-- Existing data is preserved. Operators who instead want to wipe legacy MLS state
-- can run TRUNCATE on these tables in a separate, intentional operation.

-- ============================================================
-- 1. mls_key_packages
-- ============================================================

ALTER TABLE mls_key_packages
    ADD COLUMN ciphersuite SMALLINT;

UPDATE mls_key_packages SET ciphersuite = 1 WHERE ciphersuite IS NULL;

ALTER TABLE mls_key_packages
    ALTER COLUMN ciphersuite SET NOT NULL;

-- Replace the hot-path "find an unused package" partial index so the consume
-- query can filter by ciphersuite without scanning legacy rows.
DROP INDEX IF EXISTS idx_mls_kp_user_device_unused;
CREATE INDEX idx_mls_kp_user_device_unused
    ON mls_key_packages (user_id, device_id, ciphersuite)
    WHERE consumed_at IS NULL AND last_resort = false;

-- Count index used by replenishment / low-watermark checks.
DROP INDEX IF EXISTS idx_mls_kp_count;
CREATE INDEX idx_mls_kp_count
    ON mls_key_packages (user_id, device_id, ciphersuite, consumed_at);

-- ============================================================
-- 2. mls_group_info
-- ============================================================

ALTER TABLE mls_group_info
    ADD COLUMN ciphersuite SMALLINT;

UPDATE mls_group_info SET ciphersuite = 1 WHERE ciphersuite IS NULL;

ALTER TABLE mls_group_info
    ALTER COLUMN ciphersuite SET NOT NULL;

-- Old uniqueness assumed exactly one row per (channel, type) or (server, type).
-- A ciphersuite upgrade legitimately introduces a NEW row per (scope, type) at
-- the new suite while the legacy row still exists in audit history. Re-key
-- uniqueness to include ciphersuite so a fresh current-suite group can be
-- created without conflicting with the legacy row.
DROP INDEX IF EXISTS mls_group_info_channel_type;
CREATE UNIQUE INDEX mls_group_info_channel_type
    ON mls_group_info (channel_id, group_type, ciphersuite)
    WHERE channel_id IS NOT NULL;

DROP INDEX IF EXISTS mls_group_info_server_type;
CREATE UNIQUE INDEX mls_group_info_server_type
    ON mls_group_info (server_id, group_type, ciphersuite)
    WHERE server_id IS NOT NULL;

-- ============================================================
-- 3. mls_commits
-- ============================================================

ALTER TABLE mls_commits
    ADD COLUMN ciphersuite SMALLINT;

UPDATE mls_commits SET ciphersuite = 1 WHERE ciphersuite IS NULL;

ALTER TABLE mls_commits
    ALTER COLUMN ciphersuite SET NOT NULL;

DROP INDEX IF EXISTS idx_mls_commits_channel_epoch;
CREATE INDEX idx_mls_commits_channel_epoch
    ON mls_commits (channel_id, ciphersuite, epoch);

-- ============================================================
-- 4. mls_pending_welcomes
-- ============================================================

ALTER TABLE mls_pending_welcomes
    ADD COLUMN ciphersuite SMALLINT;

UPDATE mls_pending_welcomes SET ciphersuite = 1 WHERE ciphersuite IS NULL;

ALTER TABLE mls_pending_welcomes
    ALTER COLUMN ciphersuite SET NOT NULL;

DROP INDEX IF EXISTS idx_mls_pending_welcomes_recipient;
CREATE INDEX idx_mls_pending_welcomes_recipient
    ON mls_pending_welcomes (recipient_user_id, ciphersuite);
