-- Rollback migration 000013: remove SPK history table and added columns

DROP TABLE IF EXISTS signal_spk_history;

ALTER TABLE signal_identity_keys
    DROP COLUMN IF EXISTS spk_key_id,
    DROP COLUMN IF EXISTS spk_uploaded_at;

ALTER TABLE signal_one_time_pre_keys
    DROP COLUMN IF EXISTS created_at;
