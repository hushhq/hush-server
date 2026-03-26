-- 000021_dm_and_discovery.down.sql

DROP INDEX IF EXISTS servers_discover_idx;
DROP INDEX IF EXISTS dm_pairs_user_b_idx;
DROP INDEX IF EXISTS dm_pairs_user_a_idx;
DROP TABLE IF EXISTS dm_pairs;
ALTER TABLE servers DROP CONSTRAINT IF EXISTS servers_discoverable_category_check;
ALTER TABLE servers DROP COLUMN IF EXISTS public_description;
ALTER TABLE servers DROP COLUMN IF EXISTS public_name;
ALTER TABLE servers DROP COLUMN IF EXISTS category;
ALTER TABLE servers DROP COLUMN IF EXISTS is_dm;
