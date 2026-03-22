-- Migration 000016: voice MLS group support
-- Adds group_type column to mls_group_info (composite PK: channel_id + group_type)
-- and voice_key_rotation_hours to instance_config.

-- Step 1: Drop existing single-column PK on mls_group_info.
ALTER TABLE mls_group_info DROP CONSTRAINT mls_group_info_pkey;

-- Step 2: Add group_type column. Existing rows are text channel groups → DEFAULT 'text'.
ALTER TABLE mls_group_info ADD COLUMN group_type TEXT NOT NULL DEFAULT 'text'
    CHECK (group_type IN ('text', 'voice'));

-- Step 3: Establish new composite PK (channel_id, group_type).
ALTER TABLE mls_group_info ADD PRIMARY KEY (channel_id, group_type);

-- Step 4: Add voice key rotation hours to instance_config.
-- Range: 1–168 hours (1 hour to 7 days). Default: 2 hours.
ALTER TABLE instance_config ADD COLUMN voice_key_rotation_hours INT NOT NULL DEFAULT 2
    CHECK (voice_key_rotation_hours BETWEEN 1 AND 168);
