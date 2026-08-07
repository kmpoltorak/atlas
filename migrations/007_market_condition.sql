-- Recommendation.MarketCondition (BULL/BEAR/SIDEWAYS — see internal/application/ml's
-- ClassifyMarketCondition, a deterministic ADX+EMA technical rule, not a trained model) is now
-- computed and persisted alongside every recommendation.
ALTER TABLE recommendations ADD COLUMN IF NOT EXISTS market_condition TEXT;
