-- Rename voice_mode 'performance' to 'low-latency'
UPDATE channels SET voice_mode = 'low-latency' WHERE voice_mode = 'performance';
ALTER TABLE channels DROP CONSTRAINT IF EXISTS channels_voice_mode_check;
ALTER TABLE channels ADD CONSTRAINT channels_voice_mode_check CHECK (voice_mode IS NULL OR voice_mode IN ('low-latency', 'quality'));
