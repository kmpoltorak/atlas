-- The current live price and prior session's close for each symbol (see market.Quote,
-- providers.YahooFinanceProvider.FetchQuote) — purely informational context for the Watchlist
-- page ("how is this doing today"), refreshed every alert-poll cycle for the whole watchlist
-- (see internal/app/alerts_loop.go), independent of the closed-candle bars the decision engine
-- actually acts on. Same table as name/exchange/asset_class (migrations/001): one row per
-- symbol, just more columns on it, rather than a second table joined on every read.
ALTER TABLE symbols ADD COLUMN IF NOT EXISTS last_price NUMERIC;
ALTER TABLE symbols ADD COLUMN IF NOT EXISTS previous_close NUMERIC;
ALTER TABLE symbols ADD COLUMN IF NOT EXISTS quote_updated_at TIMESTAMPTZ;

INSERT INTO schema_migrations (version) VALUES ('016') ON CONFLICT (version) DO NOTHING;
