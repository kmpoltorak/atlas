-- A real recovery-probability model now exists (see internal/application/ml.UseCase.
-- RecoveryModel, RECOVERY_MODEL_PATH), a genuinely separate, optional model from the
-- correction-probability one predictions.correction_probability already tracks. Mirrors that
-- column's NULL-means-not-computed convention: recovery_model_version is set only when a
-- recovery model actually produced this row's recovery_probability, same reasoning as
-- model_version/correction_probability in internal/infrastructure/persistence/ml.go's
-- SavePrediction (a literal 0 would misrepresent "not computed" as "computed as 0%").
ALTER TABLE predictions ADD COLUMN IF NOT EXISTS recovery_probability NUMERIC;
ALTER TABLE predictions ADD COLUMN IF NOT EXISTS recovery_model_version TEXT;
