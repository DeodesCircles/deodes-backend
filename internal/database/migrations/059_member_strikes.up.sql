-- Phase 2.7: persist per-member strike counts. ProcessMissedContributions
-- previously computed strikes in memory and dropped them; max-strikes
-- removal was unenforceable. New rows start at 0; history is unchanged.
ALTER TABLE circle_members ADD COLUMN IF NOT EXISTS strikes INT NOT NULL DEFAULT 0;
