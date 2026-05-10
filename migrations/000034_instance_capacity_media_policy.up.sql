ALTER TABLE instance_config
  ADD COLUMN max_registered_users INT DEFAULT NULL,
  ADD COLUMN screen_share_resolution_cap TEXT NOT NULL DEFAULT '1080p';

ALTER TABLE instance_config
  ADD CONSTRAINT instance_config_max_registered_users_check
    CHECK (max_registered_users IS NULL OR max_registered_users >= 1),
  ADD CONSTRAINT instance_config_screen_share_resolution_cap_check
    CHECK (screen_share_resolution_cap IN ('1080p', '720p'));
