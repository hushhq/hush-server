DROP INDEX IF EXISTS idx_attachments_channel_created_active;

ALTER TABLE instance_config
  DROP CONSTRAINT IF EXISTS instance_config_message_retention_days_check,
  DROP CONSTRAINT IF EXISTS instance_config_max_guild_attachment_storage_bytes_check,
  DROP CONSTRAINT IF EXISTS instance_config_max_attachment_bytes_check;

ALTER TABLE instance_config
  DROP COLUMN IF EXISTS message_retention_days,
  DROP COLUMN IF EXISTS max_guild_attachment_storage_bytes,
  DROP COLUMN IF EXISTS max_attachment_bytes;
