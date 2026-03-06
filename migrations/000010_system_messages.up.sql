-- Update channel type constraint to include 'system'
ALTER TABLE channels DROP CONSTRAINT channels_type_check;
ALTER TABLE channels ADD CONSTRAINT channels_type_check CHECK (type IN ('text', 'voice', 'category', 'system'));

-- Create system_messages table
CREATE TABLE system_messages (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    server_id UUID NOT NULL REFERENCES servers(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL,
    actor_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_id UUID REFERENCES users(id) ON DELETE SET NULL,
    reason TEXT NOT NULL DEFAULT '',
    metadata JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_system_messages_server_created ON system_messages(server_id, created_at DESC);

-- Add retention config to instance_config
ALTER TABLE instance_config ADD COLUMN system_message_retention_days INT DEFAULT 180;

-- Create #system channel for every existing guild
INSERT INTO channels (server_id, name, type, position)
SELECT id, 'system', 'system', -1 FROM servers;
