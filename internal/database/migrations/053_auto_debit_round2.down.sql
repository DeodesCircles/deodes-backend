DROP INDEX IF EXISTS idx_subscriptions_remind;

ALTER TABLE subscriptions
    DROP COLUMN IF EXISTS last_reminded_at;

ALTER TABLE circles
    DROP COLUMN IF EXISTS auto_debit;
