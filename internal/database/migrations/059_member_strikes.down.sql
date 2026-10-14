-- Down-only for completeness; drops the 2.7 strike counter.
ALTER TABLE circle_members DROP COLUMN IF EXISTS strikes;
