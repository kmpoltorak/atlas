-- Atlas has no real migration tool (see skills/timescaledb.md) — new migrations against an
-- existing database volume are still applied by hand (psql -f migrations/0NN_....sql), same as
-- always. This only adds a way to see what's actually been applied, which was previously
-- untracked entirely. Backfilled with every migration through this one, since 001-012 already
-- shipped and can't retroactively record themselves.
--
-- Convention from here on: every new migration file ends with its own
--   INSERT INTO schema_migrations (version) VALUES ('0NN') ON CONFLICT (version) DO NOTHING;
-- so applying it (fresh-volume auto-bootstrap or by hand against an existing one) always leaves
-- an accurate record, whichever path it went through.
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

INSERT INTO schema_migrations (version) VALUES
    ('001'), ('002'), ('003'), ('004'), ('005'), ('006'),
    ('007'), ('008'), ('009'), ('010'), ('011'), ('012'), ('013')
ON CONFLICT (version) DO NOTHING;
