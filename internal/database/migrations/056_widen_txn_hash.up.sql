-- EVM transaction hashes are 0x + 64 hex = 66 chars, but both hash
-- columns were character varying(64), so every Arc contribution/payout
-- write failed with "value too long". Widen with headroom for future
-- hash formats; existing rows are unaffected.
ALTER TABLE contributions
    ALTER COLUMN txn_hash TYPE character varying(128);

ALTER TABLE payouts
    ALTER COLUMN txn_hash TYPE character varying(128);
