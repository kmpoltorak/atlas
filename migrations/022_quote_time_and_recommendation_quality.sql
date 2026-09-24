-- Two gaps between what Atlas computed and what it kept.
--
-- symbols.quoted_at: when the market last traded at last_price, as the provider reports it.
-- quote_updated_at was the only timestamp, and it is the time Atlas *fetched* the quote. A
-- provider keeps answering with the last known price of an instrument that has stopped trading,
-- so every poll made a days-old price look minutes old and Position.IsLive never saw it age.
-- quote_updated_at keeps its meaning (fetch time); valuation reads quoted_at. Existing rows stay
-- NULL until the next poll fills them in, and a NULL quote time counts as not current.
ALTER TABLE symbols ADD COLUMN IF NOT EXISTS quoted_at TIMESTAMPTZ;

-- recommendations.degraded / risk_data_available: whether an expected input was missing, and
-- whether the risk score measured anything. Both were computed and returned by the live API, but
-- never written, so every recommendation read back from history claimed complete inputs. NULL on
-- rows written before this migration: that history did not record it and it is not invented here.
ALTER TABLE recommendations ADD COLUMN IF NOT EXISTS degraded BOOLEAN;
ALTER TABLE recommendations ADD COLUMN IF NOT EXISTS risk_data_available BOOLEAN;

-- The recovery model that fed the ML term, alongside ml_model_version (the correction model).
-- The decision has used P(recovery) since the ML term became 1 - P(correction) * (1 - P(recovery)),
-- but only one of the two model versions was ever recorded.
ALTER TABLE recommendations ADD COLUMN IF NOT EXISTS ml_recovery_model_version TEXT;

INSERT INTO schema_migrations (version) VALUES ('022') ON CONFLICT (version) DO NOTHING;
