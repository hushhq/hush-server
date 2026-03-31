-- Migration 000017: backend opacity
-- Removes plaintext guild/channel/member metadata, adds encrypted BYTEA blobs,
-- integer permission levels, guild metrics columns, access policy, and
-- instance-level guild discovery configuration.

-- ============================================================
-- 1. servers table (OPAC-01, OPAC-06)
-- ============================================================

-- Drop plaintext columns that must not exist on a blind relay.
ALTER TABLE servers DROP COLUMN name;
ALTER TABLE servers DROP COLUMN icon_url;
ALTER TABLE servers DROP COLUMN owner_id;

-- Encrypted guild metadata blob (NULL for system-created guilds before first client update).
ALTER TABLE servers ADD COLUMN encrypted_metadata BYTEA;

-- Guild metrics (billing-ready, not billing-aware).
ALTER TABLE servers ADD COLUMN member_count INT NOT NULL DEFAULT 0;
ALTER TABLE servers ADD COLUMN text_channel_count INT NOT NULL DEFAULT 0;
ALTER TABLE servers ADD COLUMN voice_channel_count INT NOT NULL DEFAULT 0;
ALTER TABLE servers ADD COLUMN storage_bytes BIGINT NOT NULL DEFAULT 0;
ALTER TABLE servers ADD COLUMN message_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE servers ADD COLUMN active_members_30d INT NOT NULL DEFAULT 0;
ALTER TABLE servers ADD COLUMN last_active_at TIMESTAMPTZ;

-- Access policy: controls who can join (open = invite-less join, request = join-request flow, closed = invite only).
ALTER TABLE servers ADD COLUMN access_policy TEXT NOT NULL DEFAULT 'open'
    CHECK (access_policy IN ('open', 'request', 'closed'));

-- Discoverable flag: instance operator can opt-in a guild for external directory.
ALTER TABLE servers ADD COLUMN discoverable BOOLEAN NOT NULL DEFAULT false;

-- Encrypted label set by the instance admin (not a Hush user) for internal ops reference.
ALTER TABLE servers ADD COLUMN admin_label_encrypted BYTEA;

-- ============================================================
-- 2. channels table (OPAC-02)
-- ============================================================

-- Drop plaintext channel name. channel.type stays plaintext for routing.
ALTER TABLE channels DROP COLUMN name;

-- Encrypted channel metadata blob (NULL for system channels; clients fall back to type for display).
ALTER TABLE channels ADD COLUMN encrypted_metadata BYTEA;

-- ============================================================
-- 3. server_members table (OPAC-03)
-- ============================================================

-- Remove string role column and its check constraint.
ALTER TABLE server_members DROP CONSTRAINT IF EXISTS server_members_role_check;
ALTER TABLE server_members DROP COLUMN role;

-- Integer permission level: 0=member, 1=mod, 2=admin, 3=owner.
-- Human-readable labels live encrypted in MLS group state; never seen by backend.
ALTER TABLE server_members ADD COLUMN permission_level INT NOT NULL DEFAULT 0
    CHECK (permission_level BETWEEN 0 AND 3);

-- ============================================================
-- 4. users table (OPAC-05)
-- ============================================================

-- Migrate existing owners to admin: the "owner" concept at instance level is
-- replaced by API key auth. Guild owner is now permission_level=3 in server_members.
UPDATE users SET role = 'admin' WHERE role = 'owner';

-- Remove the 'owner' value from the role check constraint.
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;
ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('admin', 'member'));

-- ============================================================
-- 5. instance_config table (DISC-02)
-- ============================================================

-- Remove serverCreationPolicy: creation policy is no longer an instance-level concern.
ALTER TABLE instance_config DROP COLUMN server_creation_policy;

-- Guild discovery policy for this instance: whether operators/admins can mark guilds discoverable.
ALTER TABLE instance_config ADD COLUMN guild_discovery TEXT NOT NULL DEFAULT 'allowed'
    CHECK (guild_discovery IN ('disabled', 'allowed', 'required'));

-- ============================================================
-- 6. mls_group_info table (guild metadata MLS group support)
-- ============================================================

-- Drop the existing PK so channel_id can become nullable.
-- channel_id is part of the composite PK (channel_id, group_type);
-- PostgreSQL forbids DROP NOT NULL on PK columns.
ALTER TABLE mls_group_info DROP CONSTRAINT mls_group_info_pkey;

-- Also drop the FK on channel_id - we'll re-add it after making it nullable.
ALTER TABLE mls_group_info DROP CONSTRAINT mls_group_info_channel_id_fkey;

-- Allow NULL channel_id so rows can reference a server (guild metadata group) instead.
ALTER TABLE mls_group_info ALTER COLUMN channel_id DROP NOT NULL;

-- Re-add the FK (now nullable).
ALTER TABLE mls_group_info ADD CONSTRAINT mls_group_info_channel_id_fkey
    FOREIGN KEY (channel_id) REFERENCES channels(id) ON DELETE CASCADE;

-- Add server_id reference for guild-level metadata groups.
ALTER TABLE mls_group_info ADD COLUMN server_id UUID REFERENCES servers(id) ON DELETE CASCADE;

-- Exactly one of channel_id or server_id must be set (XOR constraint).
ALTER TABLE mls_group_info ADD CONSTRAINT mls_group_info_xor_id
    CHECK ((channel_id IS NULL) != (server_id IS NULL));

-- Extend group_type to allow 'metadata' for guild-level groups.
ALTER TABLE mls_group_info DROP CONSTRAINT mls_group_info_group_type_check;
ALTER TABLE mls_group_info ADD CONSTRAINT mls_group_info_group_type_check
    CHECK (group_type IN ('text', 'voice', 'metadata'));

-- New unique index for channel-scoped rows (replaces old PK for existing use case).
CREATE UNIQUE INDEX IF NOT EXISTS mls_group_info_channel_type ON mls_group_info (channel_id, group_type)
    WHERE channel_id IS NOT NULL;

-- Unique index for server-scoped rows (guild metadata groups).
CREATE UNIQUE INDEX IF NOT EXISTS mls_group_info_server_type ON mls_group_info (server_id, group_type)
    WHERE server_id IS NOT NULL;
