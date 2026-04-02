ALTER TABLE instance_bans
    ALTER COLUMN actor_id DROP NOT NULL;

ALTER TABLE instance_bans
    ADD COLUMN IF NOT EXISTS actor_admin_id UUID REFERENCES instance_admins(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS lifted_by_admin_id UUID REFERENCES instance_admins(id) ON DELETE SET NULL;
