-- Drop system_messages table (index is dropped automatically with the table)
DROP TABLE IF EXISTS system_messages;

-- Remove retention config from instance_config
ALTER TABLE instance_config DROP COLUMN IF EXISTS system_message_retention_days;

-- Delete system channels
DELETE FROM channels WHERE type = 'system';

-- Revert channel type constraint
ALTER TABLE channels DROP CONSTRAINT channels_type_check;
ALTER TABLE channels ADD CONSTRAINT channels_type_check CHECK (type IN ('text', 'voice', 'category'));
