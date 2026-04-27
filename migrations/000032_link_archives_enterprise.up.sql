-- Migration 000032: extend link_archives + link_archive_chunks for the
-- enterprise-ready transfer plane.
--
-- Schema deltas:
--
--   * link_archives gains:
--       - user_id  (FK to users) so per-user quotas are enforceable
--       - state    (lifecycle column; replaces the boolean `finalized`
--                   at the application layer; `finalized` stays for now
--                   so existing code reads consistently during
--                   migration but the application no longer writes to
--                   it)
--
--   * link_archive_chunks gains:
--       - storage_backend kind (text enum: 'postgres_bytea' | 's3')
--       - storage_key          (opaque key the backend uses to find
--                               the bytes; string shape is per-backend)
--     and `bytes` is dropped — chunk bytes never live on the chunk
--     metadata row anymore.
--
--   * link_archive_chunk_blobs (new table):
--       - one row per stored blob in the postgres_bytea backend
--       - keyed by storage_key (matches chunk.storage_key)
--       - the s3 backend never touches this table
--
-- Self-host MVP installs that ran 000031 will see this migration drop
-- their (unused) bytes column. user_id is added NULLABLE so any
-- pre-existing rows survive the migration; application code enforces
-- non-null on inserts going forward. A later cleanup migration can
-- enforce NOT NULL once the rollout is complete.

ALTER TABLE link_archives
    ADD COLUMN user_id UUID REFERENCES users(id) ON DELETE CASCADE,
    ADD COLUMN state   TEXT NOT NULL DEFAULT 'created'
        CHECK (state IN (
            'created',
            'uploading',
            'upload_paused',
            'uploaded',
            'available',
            'importing',
            'import_paused',
            'imported',
            'acknowledged',
            'aborted',
            'expired',
            'terminal_failure'
        ));

CREATE INDEX idx_link_archives_user_state ON link_archives(user_id, state);
CREATE INDEX idx_link_archives_state      ON link_archives(state);

CREATE TABLE link_archive_chunk_blobs (
    storage_key TEXT        PRIMARY KEY,
    bytes       BYTEA       NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE link_archive_chunks
    DROP COLUMN bytes,
    ADD COLUMN storage_backend TEXT NOT NULL DEFAULT 'postgres_bytea'
        CHECK (storage_backend IN ('postgres_bytea', 's3')),
    ADD COLUMN storage_key TEXT NOT NULL DEFAULT '';

-- Self-host installs that have no rows yet keep the empty default;
-- there is nothing to backfill. Applications inserting new chunks
-- always supply a non-empty storage_key.
ALTER TABLE link_archive_chunks
    ALTER COLUMN storage_key DROP DEFAULT;
