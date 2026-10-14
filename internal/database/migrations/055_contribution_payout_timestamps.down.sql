ALTER TABLE payouts DROP COLUMN IF EXISTS created_at;

ALTER TABLE contributions DROP COLUMN IF EXISTS updated_at;
ALTER TABLE contributions DROP COLUMN IF EXISTS created_at;
