INSERT INTO server_templates (name, channels, is_default, position)
SELECT 'Default',
       '[{"name":"system","type":"system","position":-1},{"name":"general","type":"text","position":0},{"name":"General","type":"voice","voiceMode":"quality","position":1}]'::jsonb,
       true,
       0
WHERE NOT EXISTS (SELECT 1 FROM server_templates);
