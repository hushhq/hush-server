-- Migration 000032 (down): revert enterprise schema additions.

ALTER TABLE link_archive_chunks
    DROP COLUMN IF EXISTS storage_key,
    DROP COLUMN IF EXISTS storage_backend,
    ADD COLUMN IF NOT EXISTS bytes BYTEA;

DROP TABLE IF EXISTS link_archive_chunk_blobs;

DROP INDEX IF EXISTS idx_link_archives_state;
DROP INDEX IF EXISTS idx_link_archives_user_state;

ALTER TABLE link_archives
    DROP COLUMN IF EXISTS state,
    DROP COLUMN IF EXISTS user_id;
