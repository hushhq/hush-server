DELETE FROM server_templates
WHERE id IN (
    SELECT id
    FROM server_templates
    WHERE name = 'Default'
      AND is_default = true
      AND channels = '[{"name":"system","type":"system","position":-1},{"name":"general","type":"text","position":0},{"name":"General","type":"voice","voiceMode":"quality","position":1}]'::jsonb
    LIMIT 1
)
AND (SELECT COUNT(*) FROM server_templates) = 1;
