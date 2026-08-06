package persistence

import (
	"context"
	"fmt"

	"atlas/internal/domain/ml"
)

// SavePrediction appends an ML prediction record. Like recommendations, predictions are an
// append-only log keyed by (symbol, ts), not upserted state.
//
// correction_probability, expected_duration, and expected_strength (see migrations/001) match
// docs/ml.md's target ML output and stay NULL here: the real inference path
// (internal/application/ml) is still a deterministic RSI-derived stub that only produces a
// directional score, confidence, and model version (see migrations/004's score column) — not
// those richer fields, so writing to them now would fabricate data no model actually computed.
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

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO predictions (symbol_id, ts, confidence, model_version, score)
		 VALUES ($1, NOW(), $2, $3, $4)`,
		symbolID, pred.Confidence, pred.ModelVersion, pred.Score); err != nil {
		return fmt.Errorf("insert prediction for %s: %w", pred.Symbol, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
