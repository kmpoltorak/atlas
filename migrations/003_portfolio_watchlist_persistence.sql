-- Wires real persistence for the Portfolio Engine and Watchlist, closing the same
-- data-vanishes-on-restart gap that migrations 001/002 already closed for prices.

ALTER TABLE portfolios ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE positions ALTER COLUMN id SET DEFAULT gen_random_uuid();
ALTER TABLE transactions ALTER COLUMN id SET DEFAULT gen_random_uuid();

-- Atlas currently manages a single portfolio (see PROJECT_CONTEXT.md's Portfolio Engine
-- scope — no multi-user/multi-portfolio model exists yet), anchored by a fixed row upserted
-- on first write. A unique name constraint makes that upsert idempotent.
ALTER TABLE portfolios ADD CONSTRAINT portfolios_name_unique UNIQUE (name);

-- Idempotent upsert target for SavePositions: same (portfolio, symbol) overwrites in place.
ALTER TABLE positions ADD CONSTRAINT positions_portfolio_symbol_unique UNIQUE (portfolio_id, symbol_id);

-- database-final.md's ERD describes watchlists/watchlist_items, but neither table was ever
-- actually migrated until now.
CREATE TABLE IF NOT EXISTS watchlists (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS watchlist_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    watchlist_id UUID NOT NULL REFERENCES watchlists(id),
    symbol_id UUID NOT NULL REFERENCES symbols(id),
    added_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (watchlist_id, symbol_id)
);
