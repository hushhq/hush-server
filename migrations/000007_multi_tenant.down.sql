-- Reverse 000007_multi_tenant.up.sql - drop in reverse FK-safe order.

-- 8. Drop server_id from audit_log
ALTER TABLE audit_log DROP COLUMN IF EXISTS server_id;

-- 7. Drop server_id from mutes
ALTER TABLE mutes DROP COLUMN IF EXISTS server_id;

-- 6. Drop server_id from bans
ALTER TABLE bans DROP COLUMN IF EXISTS server_id;

-- 5. Drop server_creation_policy from instance_config
ALTER TABLE instance_config DROP COLUMN IF EXISTS server_creation_policy;

-- 4. Drop server_id from invite_codes
ALTER TABLE invite_codes DROP COLUMN IF EXISTS server_id;

-- 3. Drop index and server_id from channels
DROP INDEX IF EXISTS idx_channels_server_id;
ALTER TABLE channels DROP COLUMN IF EXISTS server_id;

-- 2. Drop server_members (FK to servers must go first)
DROP TABLE IF EXISTS server_members;

-- 1. Drop servers
DROP TABLE IF EXISTS servers;
