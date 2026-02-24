ALTER TABLE messages ADD COLUMN recipient_id UUID REFERENCES users(id) ON DELETE CASCADE;
CREATE INDEX idx_messages_channel_recipient_timestamp ON messages(channel_id, recipient_id, "timestamp");
