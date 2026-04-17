CREATE TABLE IF NOT EXISTS read_markers (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id        UUID        NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id           UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    read_up_to_msg_id UUID        REFERENCES messages(id) ON DELETE SET NULL,
    read_up_to_ts     TIMESTAMPTZ,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (channel_id, user_id)
);
