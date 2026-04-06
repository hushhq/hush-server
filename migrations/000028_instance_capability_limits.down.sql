ALTER TABLE instance_config
  DROP COLUMN IF EXISTS max_servers_per_user,
  DROP COLUMN IF EXISTS max_members_per_server;
