ALTER TABLE instance_config
  ADD COLUMN max_servers_per_user INT DEFAULT 1,
  ADD COLUMN max_members_per_server INT DEFAULT 12;
