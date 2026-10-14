-- Down-only for completeness; shrinking could truncate real 0x hashes,
-- so this intentionally restores the type without touching stored values.
ALTER TABLE payouts
    ALTER COLUMN txn_hash TYPE character varying(64);

ALTER TABLE contributions
    ALTER COLUMN txn_hash TYPE character varying(64);
