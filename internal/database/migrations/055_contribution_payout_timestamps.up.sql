-- Align contributions/payouts tables with the repository layer, which reads
-- and writes created_at/updated_at (contributions) and created_at (payouts).
-- The original tables used submitted_at / executed_at instead, so every
-- write failed with "column does not exist". Backfill from the legacy
-- columns, then keep them for history (harmless duplicates).
ALTER TABLE contributions
    ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

UPDATE contributions SET created_at = submitted_at WHERE created_at IS NULL;
UPDATE contributions SET updated_at = submitted_at WHERE updated_at IS NULL;

ALTER TABLE payouts
    ADD COLUMN IF NOT EXISTS created_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

UPDATE payouts SET created_at = executed_at WHERE created_at IS NULL;
