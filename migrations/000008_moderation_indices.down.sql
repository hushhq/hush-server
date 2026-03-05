-- Reverse migration: drop all indices added in 000008_moderation_indices.up.sql

DROP INDEX IF EXISTS idx_audit_log_server_created;
DROP INDEX IF EXISTS idx_audit_log_server_id;
DROP INDEX IF EXISTS idx_mutes_server_user;
DROP INDEX IF EXISTS idx_mutes_server_id;
DROP INDEX IF EXISTS idx_bans_server_user;
DROP INDEX IF EXISTS idx_bans_server_id;
