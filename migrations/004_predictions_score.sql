-- The predictions table (migrations/001) was shaped around the target ML design in docs/ml.md
-- (correction_probability, expected_duration, expected_strength). The real Go inference path
-- (internal/application/ml) is still a deterministic stub that emits a single directional
-- score, not those richer fields, so this adds a column for what is actually produced today
-- instead of writing misleading values into the target-design columns. Those columns stay
-- NULL until a real trained model produces that output.
ALTER TABLE predictions ADD COLUMN IF NOT EXISTS score NUMERIC;

-- recommendations and predictions (migrations/001) never got the id-default treatment that
-- symbols/prices (002) and portfolios/positions/transactions/watchlist_items (003) got when
-- Go code started actually inserting into them — nothing wrote to either table until now (see
-- internal/infrastructure/persistence/decision.go, ml.go). Without this, every INSERT that
-- omits id (matching the rest of the codebase's pattern) fails a NOT NULL constraint.
ALTER TABLE recommendations ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE predictions ALTER COLUMN id SET DEFAULT gen_random_uuid();
