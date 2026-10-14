-- Phase 2.6: EVM-shaped indexer state. The legacy Stellar cursor tables were
-- dropped in 057; the Arc poller tracks blocks (not ledgers) and dedups on
-- (tx_hash, log_index) per the 2.6 acceptance (the Soroban-era
-- contract_events table is append-only history and stays untouched).

-- Singleton cursor: one row per chain_id, upserted by the poller.
CREATE TABLE IF NOT EXISTS arc_cursor (
    chain_id     BIGINT      PRIMARY KEY,
    last_block   BIGINT      NOT NULL DEFAULT 0,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Decoded DeodesCircle/DeodesFactory events. Idempotent replay: the unique
-- constraint makes a duplicate (tx_hash, log_index) insert a no-op, so an
-- unfinalized range can be re-polled safely after a reorg.
CREATE TABLE IF NOT EXISTS arc_events (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tx_hash      TEXT        NOT NULL,
    log_index    BIGINT      NOT NULL,
    block_number BIGINT      NOT NULL,
    address      TEXT        NOT NULL,
    event        TEXT        NOT NULL,
    payload      JSONB       NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_arc_events_unique
    ON arc_events (tx_hash, log_index);

CREATE INDEX IF NOT EXISTS idx_arc_events_block
    ON arc_events (block_number);

CREATE INDEX IF NOT EXISTS idx_arc_events_address
    ON arc_events (address);

-- Poison/unprocessable items park here with their stage + error instead of
-- stalling the poller. An operator resolves or replays them; the poller
-- never blocks on a single bad log.
CREATE TABLE IF NOT EXISTS arc_deadletter (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    tx_hash      TEXT        NOT NULL,
    log_index    BIGINT,
    block_number BIGINT      NOT NULL DEFAULT 0,
    stage        TEXT        NOT NULL,
    error        TEXT        NOT NULL,
    payload      JSONB       NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_arc_deadletter_stage
    ON arc_deadletter (stage, created_at DESC);
