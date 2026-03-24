-- Migration 000017 (down): reverse backend opacity changes

-- ============================================================
-- 6. mls_group_info table (reverse)
-- ============================================================

DROP INDEX IF EXISTS mls_group_info_server_type;

ALTER TABLE mls_group_info DROP CONSTRAINT IF EXISTS mls_group_info_group_type_check;
ALTER TABLE mls_group_info ADD CONSTRAINT mls_group_info_group_type_check
    CHECK (group_type IN ('text', 'voice'));

ALTER TABLE mls_group_info DROP CONSTRAINT IF EXISTS mls_group_info_xor_id;
ALTER TABLE mls_group_info DROP COLUMN IF EXISTS server_id;
ALTER TABLE mls_group_info ALTER COLUMN channel_id SET NOT NULL;

-- ============================================================
-- 5. instance_config table (reverse)
-- ============================================================

ALTER TABLE instance_config DROP COLUMN IF EXISTS guild_discovery;
ALTER TABLE instance_config ADD COLUMN server_creation_policy TEXT NOT NULL DEFAULT 'open'
    CHECK (server_creation_policy IN ('open', 'invite_only'));

-- ============================================================
-- 4. users table (reverse)
-- ============================================================

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('admin', 'member', 'owner'));
-- NOTE: We cannot reliably distinguish which 'admin' users were formerly 'owner'.
-- Operators who need to restore owner status must do so manually.

-- ============================================================
-- 3. server_members table (reverse)
-- ============================================================

ALTER TABLE server_members DROP COLUMN IF EXISTS permission_level;
ALTER TABLE server_members ADD COLUMN role TEXT NOT NULL DEFAULT 'member'
    CHECK (role IN ('owner', 'admin', 'moderator', 'member'));

-- ============================================================
-- 2. channels table (reverse)
-- ============================================================

ALTER TABLE channels DROP COLUMN IF EXISTS encrypted_metadata;
ALTER TABLE channels ADD COLUMN name TEXT NOT NULL DEFAULT '';

-- ============================================================
-- 1. servers table (reverse)
-- ============================================================

ALTER TABLE servers DROP COLUMN IF EXISTS admin_label_encrypted;
ALTER TABLE servers DROP COLUMN IF EXISTS discoverable;
ALTER TABLE servers DROP COLUMN IF EXISTS access_policy;
ALTER TABLE servers DROP COLUMN IF EXISTS last_active_at;
ALTER TABLE servers DROP COLUMN IF EXISTS active_members_30d;
ALTER TABLE servers DROP COLUMN IF EXISTS message_count;
ALTER TABLE servers DROP COLUMN IF EXISTS storage_bytes;
ALTER TABLE servers DROP COLUMN IF EXISTS voice_channel_count;
ALTER TABLE servers DROP COLUMN IF EXISTS text_channel_count;
ALTER TABLE servers DROP COLUMN IF EXISTS member_count;
ALTER TABLE servers DROP COLUMN IF EXISTS encrypted_metadata;

ALTER TABLE servers ADD COLUMN name TEXT NOT NULL DEFAULT '';
ALTER TABLE servers ADD COLUMN icon_url TEXT;
ALTER TABLE servers ADD COLUMN owner_id UUID;
