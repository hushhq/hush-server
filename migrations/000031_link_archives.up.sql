-- Migration 000031: link_archives + link_archive_chunks
-- Backs the chunked device-link transfer plane.
--
-- The OLD device uploads an encrypted plaintext archive in fixed-size
-- chunks; the NEW device downloads them after the small relay envelope
-- is delivered through /api/auth/link-verify. Chunk plaintext is
-- AES-GCM ciphertext as far as the server is concerned: blind relay.
--
-- Tokens are stored as SHA-256 hashes; the raw 32-byte token bytes
-- never touch persistent storage on the server. Both expires_at and
-- hard_deadline_at are honoured at query time: rows are unreachable
-- once either has passed.

CREATE TABLE link_archives (
    id                   UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    upload_token_hash    BYTEA       NOT NULL UNIQUE,
    download_token_hash  BYTEA       NOT NULL UNIQUE,
    total_chunks         INT         NOT NULL CHECK (total_chunks > 0),
    total_bytes          BIGINT      NOT NULL CHECK (total_bytes > 0),
    chunk_size           INT         NOT NULL CHECK (chunk_size > 0),
    manifest_hash        BYTEA       NOT NULL,
    archive_sha256       BYTEA       NOT NULL,
    finalized            BOOLEAN     NOT NULL DEFAULT false,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at           TIMESTAMPTZ NOT NULL,
    hard_deadline_at     TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_link_archives_expires_at ON link_archives(expires_at);
CREATE INDEX idx_link_archives_hard_deadline ON link_archives(hard_deadline_at);

CREATE TABLE link_archive_chunks (
    archive_id  UUID        NOT NULL REFERENCES link_archives(id) ON DELETE CASCADE,
    idx         INT         NOT NULL CHECK (idx >= 0),
    bytes       BYTEA       NOT NULL,
    chunk_size  INT         NOT NULL CHECK (chunk_size > 0),
    chunk_hash  BYTEA       NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (archive_id, idx)
);
