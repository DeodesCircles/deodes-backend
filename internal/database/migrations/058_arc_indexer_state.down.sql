-- Down-only for completeness; drops the 2.6 Arc indexer state.
DROP TABLE IF EXISTS arc_deadletter;
DROP TABLE IF EXISTS arc_events;
DROP TABLE IF EXISTS arc_cursor;
