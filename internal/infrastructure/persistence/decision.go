package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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

	// ml_model_version stays NULL, not empty-string, when ML wasn't part of scoring at all
	// (NewOrchestrator without ML) — distinct from a real value naming the stub when ML ran
	// without a trained model loaded.
	var mlModelVersion any
	if rec.MLModelVersion != "" {
		mlModelVersion = rec.MLModelVersion
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO recommendations
		   (symbol_id, action, score, confidence, technical_score, ml_score, portfolio_score, risk_score, market_condition, explanation, model_version, ml_model_version)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		symbolID, rec.Action, rec.Score, rec.Confidence, rec.TechnicalScore, rec.MLScore, rec.PortfolioScore,
		rec.RiskScore, marketCondition, explanation, rec.ModelVersion, mlModelVersion); err != nil {
		return fmt.Errorf("insert recommendation for %s: %w", rec.Symbol, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// ActionStreak is when a symbol's currently recommended action was first recorded — the answer to
// "how long has this alert been saying this?", which a single recommendation row cannot give
// (each row is stamped with the moment it was computed, so the newest one always reads as "now").
type ActionStreak struct {
	Symbol string    `db:"ticker"`
	Action string    `db:"action"`
	Since  time.Time `db:"since"`
}

// CurrentActionStreaks returns, per symbol, the action it currently recommends and the moment
// that run of identical actions began.
//
// One query for every symbol rather than one per row on the page: the Alerts page renders a few
// dozen symbols, and a per-symbol round trip would make page load scale with watchlist size for
// no reason.
//
// The window function marks every row whose action differs from the previous one for that
// symbol — those are the streak boundaries — and DISTINCT ON then keeps the most recent boundary
// per symbol. A symbol's very first recorded recommendation counts as a boundary too, since
// IS DISTINCT FROM treats the NULL from LAG as a difference; that is correct, as the streak
// genuinely started there.
//
// The returned times are what the database holds, which is UTC (created_at is timestamptz,
// defaulted to now()). Callers comparing against wall-clock time should do so in UTC.
func (t *TimescaleDB) CurrentActionStreaks() (map[string]ActionStreak, error) {
	ctx := context.Background()

	var rows []ActionStreak
	if err := t.db.SelectContext(ctx, &rows, `
		WITH boundaries AS (
		    SELECT s.ticker,
		           r.action,
		           r.created_at,
		           LAG(r.action) OVER (PARTITION BY r.symbol_id ORDER BY r.created_at) AS previous_action
		    FROM recommendations r
		    JOIN symbols s ON s.id = r.symbol_id
		)
		SELECT DISTINCT ON (ticker) ticker, action, created_at AS since
		FROM boundaries
		WHERE previous_action IS DISTINCT FROM action
		ORDER BY ticker, created_at DESC`); err != nil {
		return nil, fmt.Errorf("query current action streaks: %w", err)
	}

	streaks := make(map[string]ActionStreak, len(rows))
	for _, row := range rows {
		streaks[row.Symbol] = row
	}
	return streaks, nil
}
