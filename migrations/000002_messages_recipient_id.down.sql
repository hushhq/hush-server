DROP INDEX IF EXISTS idx_messages_channel_recipient_timestamp;
ALTER TABLE messages DROP COLUMN IF EXISTS recipient_id;
