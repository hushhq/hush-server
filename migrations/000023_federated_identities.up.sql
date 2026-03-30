-- Federated identity support: allows users from remote instances to join
-- servers and send messages. Uses an XOR constraint pattern so each row
-- references exactly one identity type (local user or federated identity).

-- ============================================================
-- 1. federated_identities: cache of remote user identity data
-- ============================================================
CREATE TABLE federated_identities (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    public_key   BYTEA NOT NULL,
    home_instance TEXT NOT NULL,
    username     TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    cached_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (public_key)
);

CREATE INDEX idx_fed_ident_instance_username ON federated_identities (home_instance, username);

-- ============================================================
-- 2. server_members: polymorphic membership
-- ============================================================

-- Replace composite PK with a surrogate so we can make user_id nullable.
ALTER TABLE server_members DROP CONSTRAINT server_members_pkey;
ALTER TABLE server_members ADD COLUMN id UUID DEFAULT gen_random_uuid();
ALTER TABLE server_members ADD PRIMARY KEY (id);

-- user_id is now optional (NULL for federated members).
ALTER TABLE server_members ALTER COLUMN user_id DROP NOT NULL;

-- Foreign key to federated_identities for remote members.
ALTER TABLE server_members
    ADD COLUMN federated_identity_id UUID
    REFERENCES federated_identities(id) ON DELETE CASCADE;

-- Exactly one identity type per row.
ALTER TABLE server_members
    ADD CONSTRAINT member_identity_xor
    CHECK ((user_id IS NOT NULL) != (federated_identity_id IS NOT NULL));

-- Partial unique indexes replace the old composite PK uniqueness guarantee.
CREATE UNIQUE INDEX idx_sm_local
    ON server_members (server_id, user_id)
    WHERE user_id IS NOT NULL;

CREATE UNIQUE INDEX idx_sm_federated
    ON server_members (server_id, federated_identity_id)
    WHERE federated_identity_id IS NOT NULL;

CREATE INDEX idx_sm_federated_id ON server_members (federated_identity_id);

-- ============================================================
-- 3. messages: allow federated senders
-- ============================================================
ALTER TABLE messages ALTER COLUMN sender_id DROP NOT NULL;

ALTER TABLE messages
    ADD COLUMN federated_sender_id UUID
    REFERENCES federated_identities(id) ON DELETE CASCADE;

ALTER TABLE messages
    ADD CONSTRAINT msg_sender_xor
    CHECK ((sender_id IS NOT NULL) != (federated_sender_id IS NOT NULL));

CREATE INDEX idx_messages_fed_sender
    ON messages (federated_sender_id)
    WHERE federated_sender_id IS NOT NULL;

-- ============================================================
-- 4. mls_commits: allow federated senders
-- ============================================================
ALTER TABLE mls_commits ALTER COLUMN sender_id DROP NOT NULL;

ALTER TABLE mls_commits
    ADD COLUMN federated_sender_id UUID
    REFERENCES federated_identities(id) ON DELETE CASCADE;

ALTER TABLE mls_commits
    ADD CONSTRAINT mls_commit_sender_xor
    CHECK ((sender_id IS NOT NULL) != (federated_sender_id IS NOT NULL));

-- ============================================================
-- 5. mls_pending_welcomes: sender and recipient can be federated
-- ============================================================
ALTER TABLE mls_pending_welcomes ALTER COLUMN sender_id DROP NOT NULL;

ALTER TABLE mls_pending_welcomes
    ADD COLUMN federated_sender_id UUID
    REFERENCES federated_identities(id) ON DELETE CASCADE;

ALTER TABLE mls_pending_welcomes ALTER COLUMN recipient_user_id DROP NOT NULL;

ALTER TABLE mls_pending_welcomes
    ADD COLUMN federated_recipient_id UUID
    REFERENCES federated_identities(id) ON DELETE CASCADE;

ALTER TABLE mls_pending_welcomes
    ADD CONSTRAINT welcome_sender_xor
    CHECK ((sender_id IS NOT NULL) != (federated_sender_id IS NOT NULL));

ALTER TABLE mls_pending_welcomes
    ADD CONSTRAINT welcome_recipient_xor
    CHECK ((recipient_user_id IS NOT NULL) != (federated_recipient_id IS NOT NULL));
