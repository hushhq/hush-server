-- Reverse migration 000023_federated_identities.
-- Order matters: remove dependent constraints/columns before dropping the
-- federated_identities table that they reference.

-- ============================================================
-- 5. mls_pending_welcomes: remove federated columns
-- ============================================================
ALTER TABLE mls_pending_welcomes DROP CONSTRAINT welcome_recipient_xor;
ALTER TABLE mls_pending_welcomes DROP CONSTRAINT welcome_sender_xor;
ALTER TABLE mls_pending_welcomes ALTER COLUMN recipient_user_id SET NOT NULL;
ALTER TABLE mls_pending_welcomes DROP COLUMN federated_recipient_id;
ALTER TABLE mls_pending_welcomes ALTER COLUMN sender_id SET NOT NULL;
ALTER TABLE mls_pending_welcomes DROP COLUMN federated_sender_id;

-- ============================================================
-- 4. mls_commits: remove federated sender column
-- ============================================================
ALTER TABLE mls_commits DROP CONSTRAINT mls_commit_sender_xor;
ALTER TABLE mls_commits ALTER COLUMN sender_id SET NOT NULL;
ALTER TABLE mls_commits DROP COLUMN federated_sender_id;

-- ============================================================
-- 3. messages: remove federated sender column
-- ============================================================
DROP INDEX IF EXISTS idx_messages_fed_sender;
ALTER TABLE messages DROP CONSTRAINT msg_sender_xor;
ALTER TABLE messages ALTER COLUMN sender_id SET NOT NULL;
ALTER TABLE messages DROP COLUMN federated_sender_id;

-- ============================================================
-- 2. server_members: restore original composite PK
-- ============================================================

-- Federated rows cannot satisfy the original (server_id, user_id) PK,
-- so remove them before restoring the constraint.
DELETE FROM server_members WHERE user_id IS NULL;

DROP INDEX IF EXISTS idx_sm_federated_id;
DROP INDEX IF EXISTS idx_sm_federated;
DROP INDEX IF EXISTS idx_sm_local;

ALTER TABLE server_members DROP CONSTRAINT member_identity_xor;
ALTER TABLE server_members DROP COLUMN federated_identity_id;
ALTER TABLE server_members ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE server_members DROP CONSTRAINT server_members_pkey;
ALTER TABLE server_members DROP COLUMN id;
ALTER TABLE server_members ADD PRIMARY KEY (server_id, user_id);

-- ============================================================
-- 1. federated_identities: drop table
-- ============================================================
DROP INDEX IF EXISTS idx_fed_ident_instance_username;
DROP TABLE federated_identities;
