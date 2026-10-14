-- One payout per (circle, round): harden the application-level
-- ErrRoundAlreadyPaid guard against races. Concurrent triggers for the same
-- round now collide on this index instead of double-paying; the handler maps
-- the conflict to 409 (different recipient) or 200 duplicate (same recipient).
CREATE UNIQUE INDEX IF NOT EXISTS idx_payouts_circle_round_unique
    ON payouts (circle_id, round_number);
