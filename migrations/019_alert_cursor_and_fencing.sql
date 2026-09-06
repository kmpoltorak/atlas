-- Closes two findings from the 2026-09-06 follow-up review, which share one root cause: work that
-- must happen exactly once was coordinated by in-memory state and by a table that means something
-- else.
--
-- 1. The notification cursor was derived from `recommendations` (see seedLastActions), a table
--    that GET /recommendations, the recommendations page and post-transaction refreshes all write
--    too. So "what did we last announce" was inferred from "what did the engine last say to
--    anyone, including a browser tab". A crash between committing a recommendation and enqueuing
--    its outbox entry left history saying BUY with nothing pending, and the restart suppressed it
--    as unchanged. alert_cursor is a cursor and nothing else, written in the same transaction as
--    the outbox entry it belongs to.
--
-- 2. Nothing fenced a former leader. `Leader.IsLeader()` reads a boolean checked once before a
--    batch; a replica that lost the lease mid-poll kept writing and sending while its successor
--    started doing the same. `epoch` makes leadership monotonic, and the claim columns on
--    alert_outbox let a delivery worker take exclusive ownership of a row rather than every
--    worker reading the same pending set.

-- One row per symbol: the last action actually announced, and the outbox entry that announced it.
-- Deliberately not keyed to a portfolio or a watchlist row — a symbol removed from the watchlist
-- and re-added should not re-announce an action that was already sent.
CREATE TABLE IF NOT EXISTS alert_cursor (
    symbol        TEXT PRIMARY KEY,
    action        TEXT NOT NULL,
    outbox_id     UUID,
    announced_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- epoch increments every time the lease changes hands. A write carrying an older epoch is a write
-- from a process that has already been superseded, whether or not it knows it yet.
ALTER TABLE leader_election ADD COLUMN IF NOT EXISTS epoch BIGINT NOT NULL DEFAULT 1;

-- Claim columns. A delivery worker takes a row, sends, then records the result; a claim that goes
-- stale (the worker died mid-send) becomes available again after claim_expires_at, which is why
-- this is a lease and not a flag. Delivery is at-least-once by nature — see docs/deployment.md's
-- "Delivery guarantee" — and this bounds how often the "at least" actually happens.
ALTER TABLE alert_outbox ADD COLUMN IF NOT EXISTS claimed_by TEXT;
ALTER TABLE alert_outbox ADD COLUMN IF NOT EXISTS claim_expires_at TIMESTAMPTZ;

-- Replaces the plain undelivered index: the delivery query now also filters on the claim.
DROP INDEX IF EXISTS alert_outbox_undelivered_idx;
CREATE INDEX IF NOT EXISTS alert_outbox_claimable_idx
    ON alert_outbox (created_at)
    WHERE delivered_at IS NULL;

INSERT INTO schema_migrations (version) VALUES ('019') ON CONFLICT (version) DO NOTHING;
