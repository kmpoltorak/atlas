package persistence

import (
	"context"
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
// as "computed as 0% probability."
func (t *TimescaleDB) SavePrediction(pred ml.Prediction) error {
	ctx := context.Background()

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

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO predictions (symbol_id, ts, confidence, model_version, score, correction_probability)
		 VALUES ($1, NOW(), $2, $3, $4, $5)`,
		symbolID, pred.Confidence, pred.ModelVersion, pred.Score, correctionProbability); err != nil {
		return fmt.Errorf("insert prediction for %s: %w", pred.Symbol, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
