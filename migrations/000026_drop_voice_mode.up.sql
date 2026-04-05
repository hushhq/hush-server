-- Remove voice_mode from the product entirely.
-- Low-latency mode has been removed. All voice channels use the standard mode.

-- 1. Drop voice_mode column from channels table.
ALTER TABLE channels DROP COLUMN IF EXISTS voice_mode;

-- 2. Strip voiceMode from template JSONB data in server_templates.
-- Each row's channels column is a JSONB array of channel objects.
UPDATE server_templates
SET channels = (
    SELECT jsonb_agg(elem - 'voiceMode')
    FROM jsonb_array_elements(channels) AS elem
)
WHERE channels::text LIKE '%voiceMode%';
