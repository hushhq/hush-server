ALTER TABLE instance_config
  DROP CONSTRAINT IF EXISTS instance_config_screen_share_resolution_cap_check,
  DROP CONSTRAINT IF EXISTS instance_config_max_registered_users_check,
  DROP COLUMN IF EXISTS screen_share_resolution_cap,
  DROP COLUMN IF EXISTS max_registered_users;
