-- Enables idempotent upserts (implementation-plan.md Phase 2: "collector service with
-- idempotent persistence"). PG16 has gen_random_uuid() built into core, so no extension
-- is needed and the application doesn't have to generate/pass UUIDs itself.

ALTER TABLE symbols ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE prices ALTER COLUMN id SET DEFAULT gen_random_uuid();

ALTER TABLE prices ADD CONSTRAINT prices_symbol_ts_unique UNIQUE (symbol_id, ts);
