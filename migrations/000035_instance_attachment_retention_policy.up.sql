ALTER TABLE instance_config
  ADD COLUMN max_attachment_bytes BIGINT NOT NULL DEFAULT 26214400,
  ADD COLUMN max_guild_attachment_storage_bytes BIGINT DEFAULT NULL,
  ADD COLUMN message_retention_days INT NOT NULL DEFAULT 90;

ALTER TABLE instance_config
  ADD CONSTRAINT instance_config_max_attachment_bytes_check
    CHECK (max_attachment_bytes >= 1),
  ADD CONSTRAINT instance_config_max_guild_attachment_storage_bytes_check
    CHECK (
      max_guild_attachment_storage_bytes IS NULL
      OR max_guild_attachment_storage_bytes >= 1
    ),
  ADD CONSTRAINT instance_config_message_retention_days_check
    CHECK (message_retention_days >= 1);

CREATE INDEX idx_attachments_channel_created_active
  ON attachments(channel_id, created_at)
  WHERE deleted_at IS NULL;
