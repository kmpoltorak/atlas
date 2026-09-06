-- Three things the 2026-09-05 review asked for, all of which are about a claim the database
-- should be enforcing rather than trusting the application to have got right.
--
-- 1. CHECK constraints on the money columns. Application validation already rejects a negative
--    or non-finite quantity (see portfolio.UseCase.RecordTransaction), but the database is the
--    only place that also covers rows written by a restore, a manual psql session, or a future
--    code path that forgets. NOT VALID + VALIDATE keeps this from taking an ACCESS EXCLUSIVE
--    lock for the length of a full table scan on a live table.
-- 2. A leader lease, so exactly one process polls the watchlist and sends notifications no
--    matter how many replicas are running (see app.Leader).
-- 3. A durable alert outbox, replacing the in-memory retry map that lost every undelivered
--    notification on restart (see app.pollAlertsOnce's old `pending`).

-- Idempotent by construction: Postgres has no ADD CONSTRAINT IF NOT EXISTS, and a migration
-- that fails when re-run is exactly the footgun the runner exists to avoid.
DO $$
DECLARE
    spec RECORD;
BEGIN
    FOR spec IN
        SELECT * FROM (VALUES
            ('transactions', 'transactions_quantity_positive',   'quantity > 0'),
            ('transactions', 'transactions_price_positive',      'price > 0'),
            ('transactions', 'transactions_price_in_pln_positive','price_in_pln > 0'),
            ('transactions', 'transactions_type_known',          'type IN (''BUY'', ''SELL'')'),
            -- Positions are derived state, recomputed from the ledger, and a zero-quantity row is
            -- deleted rather than kept (see portfolio.UseCase.applyPositionUpdate) — hence > 0.
            ('positions',    'positions_quantity_positive',      'quantity > 0'),
            ('positions',    'positions_avg_cost_positive',      'avg_cost > 0')
        ) AS t(table_name, constraint_name, expression)
    LOOP
        IF NOT EXISTS (
            SELECT 1 FROM pg_constraint WHERE conname = spec.constraint_name
        ) THEN
            EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I CHECK (%s) NOT VALID',
                           spec.table_name, spec.constraint_name, spec.expression);
        END IF;
        -- VALIDATE is the part that scans, and it takes only a SHARE UPDATE EXCLUSIVE lock, so
        -- concurrent reads and writes keep working. A pre-existing bad row fails the migration
        -- loudly here, which is the point: it means data the application believes impossible
        -- already exists.
        EXECUTE format('ALTER TABLE %I VALIDATE CONSTRAINT %I',
                       spec.table_name, spec.constraint_name);
    END LOOP;
END $$;

-- Exactly one row, ever (id is a constant TRUE, same single-row trick as backup_log). holder is
-- a per-process identity, not a hostname: two pods can share a hostname, and a restarted process
-- must not inherit its own previous lease without re-acquiring it.
CREATE TABLE IF NOT EXISTS leader_election (
    id          BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (id),
    holder      TEXT NOT NULL,
    acquired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL
);

-- The alert outbox. One row per action transition worth notifying about, created before any
-- delivery is attempted, so a crash between "decided to notify" and "notified" leaves a record
-- to retry from instead of silence.
--
-- pending_channels lists the channels that still owe this alert, by name, comma-separated. It
-- starts as every configured channel and shrinks as each one succeeds, which is what makes a
-- partial failure retry only the channels that actually failed rather than re-notifying the ones
-- that already delivered. delivered_at is set when it empties. TEXT rather than TEXT[] because
-- channel names never contain a comma and the array type would have meant a new Go driver
-- dependency to read one column.
CREATE TABLE IF NOT EXISTS alert_outbox (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    symbol           TEXT NOT NULL,
    action           TEXT NOT NULL,
    payload          JSONB NOT NULL,
    pending_channels TEXT NOT NULL DEFAULT '',
    attempts         INTEGER NOT NULL DEFAULT 0,
    last_error       TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at     TIMESTAMPTZ
);

-- The only query the delivery loop runs: undelivered rows, oldest first.
CREATE INDEX IF NOT EXISTS alert_outbox_undelivered_idx
    ON alert_outbox (created_at)
    WHERE delivered_at IS NULL;

INSERT INTO schema_migrations (version) VALUES ('018') ON CONFLICT (version) DO NOTHING;
