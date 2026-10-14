-- Purge program: the Stellar/Horizon indexer was deleted (Arc-EVM only).
-- Its durable state (cursor + dead-letter queue) is unreachable dead weight:
-- no Go code references either table. Drop them.
DROP TABLE IF EXISTS indexer_dead_letter;
DROP TABLE IF EXISTS indexer_cursor;
