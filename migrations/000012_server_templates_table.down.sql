ALTER TABLE instance_config ADD COLUMN IF NOT EXISTS server_template JSONB
    DEFAULT '[{"name":"system","type":"system","position":-1},{"name":"general","type":"text","position":0},{"name":"General","type":"voice","voiceMode":"quality","position":1}]'::jsonb;

-- Migrate default template back
UPDATE instance_config SET server_template = (
    SELECT channels FROM server_templates WHERE is_default = true LIMIT 1
);

DROP TABLE IF EXISTS server_templates;
