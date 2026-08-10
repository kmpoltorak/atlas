-- The Alerts page reports how long each symbol has held its current recommended action, which
-- means walking that symbol's recommendation history backwards to where the action last changed
-- (see persistence.CurrentActionStreaks). Recommendations are append-only and every poll writes
-- one row per watchlist symbol, so the table grows by roughly 47 rows every five minutes —
-- without an index that walk degrades into a full scan on every page load.
--
-- (symbol_id, created_at DESC) is the exact shape of the window that query partitions and orders
-- by. The primary key is (id, ts) and does nothing for this access pattern.
CREATE INDEX IF NOT EXISTS recommendations_symbol_created_idx
    ON recommendations (symbol_id, created_at DESC);

INSERT INTO schema_migrations (version) VALUES ('014') ON CONFLICT (version) DO NOTHING;
