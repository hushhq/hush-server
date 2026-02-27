-- Remove category channels and restore original constraint.
DELETE FROM channels WHERE type = 'category';
ALTER TABLE channels DROP CONSTRAINT channels_type_check;
ALTER TABLE channels ADD CONSTRAINT channels_type_check CHECK (type IN ('text', 'voice'));
