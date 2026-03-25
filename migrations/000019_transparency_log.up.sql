-- Transparency log: append-only table for key lifecycle events.
-- Each row corresponds to one leaf in the Merkle tree.
CREATE TABLE transparency_log_entries (
    id           BIGSERIAL   PRIMARY KEY,
    leaf_index   BIGINT      NOT NULL UNIQUE,
    operation    TEXT        NOT NULL CHECK (operation IN (
                     'register', 'device_add', 'device_revoke',
                     'keypackage', 'mls_credential', 'account_recovery')),
    user_pub_key BYTEA       NOT NULL,
    subject_key  BYTEA,
    entry_cbor   BYTEA       NOT NULL,
    leaf_hash    BYTEA       NOT NULL,
    user_sig     BYTEA       NOT NULL,
    log_sig      BYTEA       NOT NULL,
    logged_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tlog_user_pub_key ON transparency_log_entries(user_pub_key);
CREATE INDEX idx_tlog_logged_at    ON transparency_log_entries(logged_at);

-- Tree heads: one row per Merkle tree state after each append batch.
-- The fringe column stores the right-edge sibling hashes for O(log N) recovery.
CREATE TABLE transparency_tree_heads (
    tree_size  BIGINT      PRIMARY KEY,
    root_hash  BYTEA       NOT NULL,
    fringe     BYTEA       NOT NULL,
    head_sig   BYTEA       NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
