-- Revert voice_mode 'low-latency' back to 'performance'
UPDATE channels SET voice_mode = 'performance' WHERE voice_mode = 'low-latency';
ALTER TABLE channels DROP CONSTRAINT IF EXISTS channels_voice_mode_check;
ALTER TABLE channels ADD CONSTRAINT channels_voice_mode_check CHECK (voice_mode IS NULL OR voice_mode IN ('performance', 'quality'));
