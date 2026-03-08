ALTER TABLE instance_config ADD COLUMN server_template JSONB
    DEFAULT '[{"name":"system","type":"system","position":-1},{"name":"general","type":"text","position":0},{"name":"General","type":"voice","voiceMode":"quality","position":1}]'::jsonb;
