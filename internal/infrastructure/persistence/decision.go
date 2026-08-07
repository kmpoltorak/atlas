package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	"atlas/internal/domain/decision"
)

// SaveRecommendation appends a recommendation record for later history/audit queries.
// Recommendations are an append-only decision log (like transactions), not upserted state —
// each call to the decision engine is its own row, even for the same symbol.
func (t *TimescaleDB) SaveRecommendation(rec decision.Recommendation) error {
	ctx := context.Background()

	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded below

	symbolID, err := upsertSymbol(ctx, tx, rec.Symbol)
	if err != nil {
		return err
	}

	explanation, err := json.Marshal(rec.Reason)
	if err != nil {
		return fmt.Errorf("marshal recommendation reason for %s: %w", rec.Symbol, err)
	}

	// market_condition stays NULL, not empty-string, when ClassifyMarketCondition couldn't
	// compute one (insufficient history) — "unknown" and "computed as empty" are different
	// claims.
	var marketCondition any
	if rec.MarketCondition != "" {
		marketCondition = rec.MarketCondition
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO recommendations
		   (symbol_id, action, score, confidence, technical_score, ml_score, portfolio_score, risk_score, market_condition, explanation, model_version)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		symbolID, rec.Action, rec.Score, rec.Confidence, rec.TechnicalScore, rec.MLScore, rec.PortfolioScore,
		rec.RiskScore, marketCondition, explanation, rec.ModelVersion); err != nil {
		return fmt.Errorf("insert recommendation for %s: %w", rec.Symbol, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
