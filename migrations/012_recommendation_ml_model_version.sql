-- Recommendations already store model_version (the decision-engine's own scoring-logic tag,
-- e.g. "atlas-decision-v1") but never which real ML model actually produced ml_score — this
-- adds that for full traceability (docs/decision_engine.md's "Keep recommendation inputs
-- traceable"). NULL when ML wasn't part of scoring at all, distinct from a real value naming
-- the stub (ml.StubModelVersion) when ML ran without a trained model loaded.
ALTER TABLE recommendations ADD COLUMN IF NOT EXISTS ml_model_version TEXT;
