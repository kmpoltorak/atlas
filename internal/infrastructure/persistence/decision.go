package persistence

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"atlas/internal/domain/decision"
)

// SaveRecommendation appends a recommendation record for later history/audit queries.
// Recommendations are an append-only decision log (like transactions), not upserted state —
// each call to the decision engine is its own row, even for the same symbol.
func (t *TimescaleDB) SaveRecommendation(rec decision.Recommendation) error {
	ctx, cancel := t.context()
	defer cancel()

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
	ctx, cancel := t.context()
	defer cancel()

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

// latestRecommendationRow mirrors the recommendations table's columns for LatestRecommendations
// below. market_condition/ml_model_version are nullable in the schema (see migrations/007 and
// 012 — added after the table's original columns, and left NULL rather than empty-string when
// ClassifyMarketCondition/ML scoring didn't run — see decision.Recommendation's own doc
// comments), so they're scanned as sql.NullString rather than plain string.
type latestRecommendationRow struct {
	Symbol          string          `db:"ticker"`
	Action          string          `db:"action"`
	Score           float64         `db:"score"`
	Confidence      float64         `db:"confidence"`
	TechnicalScore  float64         `db:"technical_score"`
	MLScore         float64         `db:"ml_score"`
	PortfolioScore  float64         `db:"portfolio_score"`
	RiskScore       float64         `db:"risk_score"`
	MarketCondition sql.NullString  `db:"market_condition"`
	Explanation     json.RawMessage `db:"explanation"`
	ModelVersion    string          `db:"model_version"`
	MLModelVersion  sql.NullString  `db:"ml_model_version"`
	CreatedAt       time.Time       `db:"created_at"`
}

// LatestRecommendation pairs a persisted decision.Recommendation with when it was computed —
// decision.Recommendation itself carries no timestamp (a single recommendation object is used
// both for a request's immediate response and for the historical log, and only the latter needs
// one), so LatestRecommendations attaches it alongside rather than growing that domain type for
// one caller's benefit.
type LatestRecommendation struct {
	decision.Recommendation
	CreatedAt time.Time
}

// LatestRecommendations returns, per symbol, the most recently persisted recommendation — the
// same row the background alert-poll loop just wrote (see internal/app/alerts_loop.go's
// pollAlertsOnce, which persists one every ALERT_POLL_INTERVAL_SECONDS for every watchlist symbol
// whose action isn't HOLD; a symbol that evaluates to HOLD gets no row that poll, by design —
// see alerts.UseCase.Evaluate).
//
// One consequence worth being explicit about: because HOLD is never itself persisted by the poll
// loop, a symbol that goes quiet after alerting keeps showing its last real action here until
// either a new non-HOLD action overwrites it or something else persists a HOLD row for that
// symbol (e.g. a manual /recommendations?symbol= lookup) — the row can lag the live signal by
// more than one poll in that specific case. This is the same property CurrentActionStreaks above
// and the dashboard's alert-count card (web_handlers.go's alertCountsByAction) already accepted;
// this function does not make it worse, but does not fix it either.
//
// This lets the Alerts page/API serve from what the last poll already computed instead of
// re-running the full technical+ML+portfolio+risk pipeline for every watchlist symbol on every
// request — the same fix already applied to the dashboard's alert-count card (see
// internal/app/web_handlers.go's alertCountsByAction) for the same reason: with the bars cache
// and the poll interval both defaulting to 300s, a request landing in the gap paid for up to 47
// sequential provider fetches plus ML inference inline, measured at p90 8.4s / p99 >10s on live
// traffic.
func (t *TimescaleDB) LatestRecommendations() (map[string]LatestRecommendation, error) {
	ctx, cancel := t.context()
	defer cancel()

	var rows []latestRecommendationRow
	if err := t.db.SelectContext(ctx, &rows, `
		SELECT DISTINCT ON (s.ticker)
		       s.ticker, r.action, r.score, r.confidence, r.technical_score, r.ml_score,
		       r.portfolio_score, r.risk_score, r.market_condition, r.explanation,
		       r.model_version, r.ml_model_version, r.created_at
		FROM recommendations r
		JOIN symbols s ON s.id = r.symbol_id
		ORDER BY s.ticker, r.created_at DESC`); err != nil {
		return nil, fmt.Errorf("query latest recommendations: %w", err)
	}

	result := make(map[string]LatestRecommendation, len(rows))
	for _, row := range rows {
		var reason []string
		if err := json.Unmarshal(row.Explanation, &reason); err != nil {
			return nil, fmt.Errorf("unmarshal recommendation reason for %s: %w", row.Symbol, err)
		}
		result[row.Symbol] = LatestRecommendation{
			Recommendation: decision.Recommendation{
				Symbol:          row.Symbol,
				Action:          row.Action,
				Score:           row.Score,
				Confidence:      row.Confidence,
				TechnicalScore:  row.TechnicalScore,
				MLScore:         row.MLScore,
				PortfolioScore:  row.PortfolioScore,
				RiskScore:       row.RiskScore,
				MarketCondition: row.MarketCondition.String,
				Reason:          reason,
				ModelVersion:    row.ModelVersion,
				MLModelVersion:  row.MLModelVersion.String,
			},
			CreatedAt: row.CreatedAt,
		}
	}
	return result, nil
}
