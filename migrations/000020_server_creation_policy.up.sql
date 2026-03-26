ALTER TABLE instance_config
    ADD COLUMN server_creation_policy TEXT NOT NULL DEFAULT 'open'
        CHECK (server_creation_policy IN ('open', 'paid', 'disabled'));
