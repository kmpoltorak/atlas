package persistence

import (
	"testing"

	"atlas/internal/domain/decision"
)

func TestSaveRecommendationInsertsRecord(t *testing.T) {
	db := connectTestDB(t)

	rec := decision.Recommendation{
		Symbol: "TESTREC", Action: "BUY", Confidence: 0.78,
		Reason:         []string{"Momentum signal is favorable", "ML signal supportive"},
		Score:          0.81,
		TechnicalScore: 0.9,
		MLScore:        0.7,
		PortfolioScore: 0.5,
		RiskScore:      0.6,
		ModelVersion:   "atlas-decision-v1",
	}
	if err := db.SaveRecommendation(rec); err != nil {
		t.Fatalf("SaveRecommendation failed: %v", err)
	}

	var row struct {
		Action         string  `db:"action"`
		Score          float64 `db:"score"`
		Confidence     float64 `db:"confidence"`
		TechnicalScore float64 `db:"technical_score"`
		MLScore        float64 `db:"ml_score"`
		PortfolioScore float64 `db:"portfolio_score"`
		RiskScore      float64 `db:"risk_score"`
		ModelVersion   string  `db:"model_version"`
	}
	if err := db.db.Get(&row, `
		SELECT r.action, r.score, r.confidence, r.technical_score, r.ml_score, r.portfolio_score, r.risk_score, r.model_version
		FROM recommendations r
		JOIN symbols s ON s.id = r.symbol_id
		WHERE s.ticker = $1
		ORDER BY r.created_at DESC LIMIT 1`, "TESTREC"); err != nil {
		t.Fatalf("failed to read back recommendation: %v", err)
	}
	if row.Action != "BUY" || row.Score != 0.81 || row.Confidence != 0.78 ||
		row.TechnicalScore != 0.9 || row.MLScore != 0.7 || row.PortfolioScore != 0.5 ||
		row.RiskScore != 0.6 || row.ModelVersion != "atlas-decision-v1" {
		t.Fatalf("unexpected stored recommendation: %+v", row)
	}
}

func TestSaveRecommendationAllowsMultipleRowsPerSymbol(t *testing.T) {
	db := connectTestDB(t)

	countForSymbol := func() int {
		var count int
		if err := db.db.Get(&count, `
			SELECT COUNT(*) FROM recommendations r
			JOIN symbols s ON s.id = r.symbol_id
			WHERE s.ticker = $1`, "TESTRECHIST"); err != nil {
			t.Fatalf("failed to count recommendations: %v", err)
		}
		return count
	}
	before := countForSymbol()

	for _, action := range []string{"HOLD", "BUY"} {
		if err := db.SaveRecommendation(decision.Recommendation{Symbol: "TESTRECHIST", Action: action, Confidence: 0.6}); err != nil {
			t.Fatalf("SaveRecommendation(%s) failed: %v", action, err)
		}
	}

	// Recommendations are an append-only log (unlike positions), so re-running this test
	// against a persistent DB should add exactly 2 new rows, not overwrite in place — assert
	// the delta rather than an absolute count so repeated runs against the same DB don't fail.
	if got := countForSymbol() - before; got != 2 {
		t.Fatalf("expected 2 new append-only rows, got %d", got)
	}
}
