-- indicators (migrations/001) never got the id-default treatment that recommendations/
-- predictions got in 004 when Go code started actually inserting into them — nothing wrote to
-- it until now (see internal/infrastructure/persistence/indicators.go). Without this, every
-- INSERT that omits id (matching the rest of the codebase's pattern) fails a NOT NULL
-- constraint.
ALTER TABLE indicators ALTER COLUMN id SET DEFAULT gen_random_uuid();
