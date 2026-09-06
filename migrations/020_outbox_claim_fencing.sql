-- A claim needs a generation, not just an owner.
--
-- claimed_by alone cannot fence an acknowledgement: the holder id is the process identity, which
-- is stable across claims. A worker whose claim expired mid-send, whose row was then taken and
-- completed by a successor, and which finally reports its own result, matches on claimed_by if it
-- happens to reclaim the row later — and its stale result overwrites the successor's.
--
-- claim_seq increments on every claim, so an acknowledgement can name the exact claim it belongs
-- to. A write from a superseded claim matches zero rows and is reported as lost rather than
-- landing late. This is what keeps at-least-once from degrading into "newer delivery state can be
-- replaced by older".
ALTER TABLE alert_outbox ADD COLUMN IF NOT EXISTS claim_seq BIGINT NOT NULL DEFAULT 0;

INSERT INTO schema_migrations (version) VALUES ('020') ON CONFLICT (version) DO NOTHING;
