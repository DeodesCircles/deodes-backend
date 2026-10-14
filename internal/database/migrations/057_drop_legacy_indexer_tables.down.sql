-- Down-only for completeness; restores the pre-purge indexer tables empty.
-- The indexer itself is gone, so rolling back here only resurrects schema.
CREATE TABLE IF NOT EXISTS indexer_cursor (
    id SERIAL PRIMARY KEY,
    chain VARCHAR(20) NOT NULL DEFAULT 'arc',
    last_ledger BIGINT NOT NULL DEFAULT 0,
    last_processed_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS indexer_dead_letter (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    chain        VARCHAR(50) NOT NULL DEFAULT 'arc',
    tx_hash      TEXT        NOT NULL,
    ledger       BIGINT      NOT NULL,
    error        TEXT        NOT NULL,
    attempts     INT         NOT NULL DEFAULT 1,
    payload      JSONB,
    status       VARCHAR(20) NOT NULL DEFAULT 'dead_letter',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at  TIMESTAMPTZ
);
