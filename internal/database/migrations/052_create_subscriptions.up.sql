-- Auto-debit subscriptions for "set and forget" savings circles.
--
-- A subscription authorizes the keeper to pull one member's contribution on
-- the circle schedule. Consent is explicit, capped, and revocable: status
-- moves active -> paused -> active, or active -> cancelled/completed.
-- next_debit_at is advanced by the keeper after every settled debit using the
-- circle's frequency (daily/weekly/biweekly/monthly); the keeper only ever
-- selects rows with status 'active' and next_debit_at <= NOW().
--
-- collected_total enforces the consent cap off-chain: the keeper skips (and
-- completes the subscription when reached) rather than debiting past it.
-- last_round makes debits idempotent per (circle, round) together with the
-- contributions UNIQUE(circle_id, user_id, round_number) constraint.

CREATE TABLE IF NOT EXISTS subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    circle_id UUID NOT NULL REFERENCES circles(id) ON DELETE CASCADE,
    amount NUMERIC(18,7) NOT NULL CHECK (amount > 0),
    cap_total NUMERIC(18,7) NOT NULL CHECK (cap_total > 0),
    collected_total NUMERIC(18,7) NOT NULL DEFAULT 0 CHECK (collected_total >= 0),
    status VARCHAR(20) NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'paused', 'cancelled', 'completed')),
    next_debit_at TIMESTAMPTZ NOT NULL,
    last_debit_at TIMESTAMPTZ,
    last_round INTEGER NOT NULL DEFAULT 0 CHECK (last_round >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(user_id, circle_id)
);

-- Serves the keeper's due query: active subscriptions past their debit time.
CREATE INDEX IF NOT EXISTS idx_subscriptions_due
    ON subscriptions(status, next_debit_at)
    WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_subscriptions_user
    ON subscriptions(user_id);

CREATE INDEX IF NOT EXISTS idx_subscriptions_circle
    ON subscriptions(circle_id);
