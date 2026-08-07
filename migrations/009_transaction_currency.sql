-- Multi-currency transaction recording (see internal/domain/currency, internal/application/
-- exchangerate). Every transaction now carries the currency it was actually entered in and its
-- PLN-equivalent price at the moment it was recorded — position math (portfolio.UseCase.
-- applyTransaction) always uses price_in_pln, never price directly, so a symbol bought partly
-- in USD and partly in PLN still has one mathematically coherent cost basis.
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS currency TEXT NOT NULL DEFAULT 'PLN';
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS price_in_pln NUMERIC;

-- Backfill: every transaction recorded before this migration was implicitly PLN (multi-currency
-- didn't exist yet), so its PLN-equivalent price is just its price.
UPDATE transactions SET price_in_pln = price WHERE price_in_pln IS NULL;

ALTER TABLE transactions ALTER COLUMN price_in_pln SET NOT NULL;
