-- Re-add voice_mode column with the original constraint shape.
ALTER TABLE channels ADD COLUMN voice_mode TEXT
    CHECK (voice_mode IS NULL OR voice_mode IN ('low-latency', 'quality'));
