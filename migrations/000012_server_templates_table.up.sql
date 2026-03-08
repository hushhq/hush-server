CREATE TABLE server_templates (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(100) NOT NULL,
    channels JSONB NOT NULL DEFAULT '[]'::jsonb,
    is_default BOOLEAN NOT NULL DEFAULT false,
    position INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Migrate existing template from instance_config (if any) as "Default"
INSERT INTO server_templates (name, channels, is_default, position)
SELECT 'Default',
       COALESCE(server_template, '[{"name":"system","type":"system","position":-1},{"name":"general","type":"text","position":0},{"name":"General","type":"voice","voiceMode":"quality","position":1}]'::jsonb),
       true, 0
FROM instance_config
LIMIT 1;

-- If no instance_config row existed, seed a default template anyway
INSERT INTO server_templates (name, channels, is_default, position)
SELECT 'Default',
       '[{"name":"system","type":"system","position":-1},{"name":"general","type":"text","position":0},{"name":"General","type":"voice","voiceMode":"quality","position":1}]'::jsonb,
       true, 0
WHERE NOT EXISTS (SELECT 1 FROM server_templates);

-- Drop the old column from instance_config
ALTER TABLE instance_config DROP COLUMN IF EXISTS server_template;
