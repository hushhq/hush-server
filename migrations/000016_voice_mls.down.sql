-- Migration 000016 rollback: restore original mls_group_info PK,
-- remove group_type column, and remove voice_key_rotation_hours.

-- Step 1: Remove voice_key_rotation_hours from instance_config.
ALTER TABLE instance_config DROP COLUMN IF EXISTS voice_key_rotation_hours;

-- Step 2: Drop composite PK.
ALTER TABLE mls_group_info DROP CONSTRAINT mls_group_info_pkey;

-- Step 3: Remove group_type column. Any voice group rows are discarded (ephemeral anyway).
ALTER TABLE mls_group_info DROP COLUMN IF EXISTS group_type;

-- Step 4: Restore original single-column PK on channel_id.
ALTER TABLE mls_group_info ADD PRIMARY KEY (channel_id);
