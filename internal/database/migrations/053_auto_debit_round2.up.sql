-- Auto-debit round 2: circle mode flag + reminder tracking.
--
-- circles.auto_debit marks Auto circles: members consent once and the keeper
-- pulls each round. Flexible circles (false, the default) stay manual.
-- Subscription creation is only accepted for Auto circles, so the flag is
-- the organizer's explicit opt-in for the whole group.
--
-- subscriptions.last_reminded_at lets the reminder sweep notify once per
-- upcoming debit instead of every keeper tick.

ALTER TABLE circles
    ADD COLUMN IF NOT EXISTS auto_debit BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE subscriptions
    ADD COLUMN IF NOT EXISTS last_reminded_at TIMESTAMPTZ;

-- Serves the reminder sweep: active subscriptions due within the window that
-- have not been reminded yet (or whose debit moved since the last reminder).
CREATE INDEX IF NOT EXISTS idx_subscriptions_remind
    ON subscriptions(status, next_debit_at)
    WHERE status = 'active';
