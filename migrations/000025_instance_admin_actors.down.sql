ALTER TABLE instance_bans
    DROP COLUMN IF EXISTS lifted_by_admin_id,
    DROP COLUMN IF EXISTS actor_admin_id;

ALTER TABLE instance_bans
    ALTER COLUMN actor_id SET NOT NULL;
