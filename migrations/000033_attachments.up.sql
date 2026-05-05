-- Migration 000033: attachments
-- Backs the per-channel encrypted attachment plane.
--
-- Each row records one client-side encrypted blob uploaded directly to
-- the configured storage backend (S3-compatible). The backend never
-- sees the AES-GCM key or IV — those live inside the MLS-encrypted
-- message envelope and are sent peer-to-peer via the application
-- ciphertext on `messages.ciphertext`.
--
-- size is the *ciphertext* byte size as written to storage (used for
-- per-instance quota accounting and for streaming buffer sizing on the
-- read path). content_type is advisory only — the bytes themselves are
-- opaque to the server.
--
-- Soft-delete (deleted_at) decouples row removal from blob removal so
-- the supervised purger can verify Backend.Delete success before
-- dropping the row, and so audit/log queries can include
-- already-deleted attachments without resurrecting them.

CREATE TABLE attachments (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id    UUID        NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    owner_id      UUID        NOT NULL REFERENCES users(id),
    storage_key   TEXT        NOT NULL,
    size          BIGINT      NOT NULL CHECK (size > 0),
    content_type  TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at    TIMESTAMPTZ
);

CREATE INDEX idx_attachments_channel
    ON attachments(channel_id)
    WHERE deleted_at IS NULL;

CREATE INDEX idx_attachments_owner
    ON attachments(owner_id)
    WHERE deleted_at IS NULL;
