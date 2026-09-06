package persistence

import (
	"fmt"

	"atlas/internal/domain/ml"
)

// SavePrediction appends an ML prediction record. Like recommendations, predictions are an
// append-only log keyed by (symbol, ts), not upserted state.
//
// expected_duration and expected_strength (see migrations/001) match docs/ml.md's target ML
// output and stay NULL: no loaded model produces those yet, only a correction probability (see
// training/train.py). correction_probability itself is only written when a real model produced
// it — pred.CorrectionProbability is the unset zero value for the deterministic RSI-derived
// stub (ml.StubModelVersion), and writing a literal 0 there would misrepresent "not computed"
// as "computed as 0% probability." recovery_probability/recovery_model_version (migrations/010)
// follow the identical convention, gated on pred.RecoveryModelVersion instead — a genuinely
// separate, independently-optional model from the correction one above.
func (t *TimescaleDB) SavePrediction(pred ml.Prediction) error {
	ctx, cancel := t.context()
	defer cancel()

	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded below

	symbolID, err := upsertSymbol(ctx, tx, pred.Symbol)
	if err != nil {
		return err
	}

	var correctionProbability any
	if pred.ModelVersion != ml.StubModelVersion {
		correctionProbability = pred.CorrectionProbability
	}

	var recoveryProbability, recoveryModelVersion any
	if pred.RecoveryModelVersion != "" {
		recoveryProbability = pred.RecoveryProbability
		recoveryModelVersion = pred.RecoveryModelVersion
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO predictions (symbol_id, ts, confidence, model_version, score, correction_probability, recovery_probability, recovery_model_version)
		 VALUES ($1, NOW(), $2, $3, $4, $5, $6, $7)`,
		symbolID, pred.Confidence, pred.ModelVersion, pred.Score, correctionProbability, recoveryProbability, recoveryModelVersion); err != nil {
		return fmt.Errorf("insert prediction for %s: %w", pred.Symbol, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
