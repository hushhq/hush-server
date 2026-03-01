-- Allow 'category' as a channel type alongside 'text' and 'voice'.
ALTER TABLE channels DROP CONSTRAINT channels_type_check;
ALTER TABLE channels ADD CONSTRAINT channels_type_check CHECK (type IN ('text', 'voice', 'category'));
