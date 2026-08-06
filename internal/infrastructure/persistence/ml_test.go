package persistence

import (
	"testing"

	"atlas/internal/domain/ml"
)

func TestSavePredictionInsertsRecord(t *testing.T) {
	db := connectTestDB(t)

	pred := ml.Prediction{Symbol: "TESTPRED", Score: 0.62, Confidence: 0.72, ModelVersion: "stub-v0"}
	if err := db.SavePrediction(pred); err != nil {
		t.Fatalf("SavePrediction failed: %v", err)
	}

	var row struct {
		Score        float64 `db:"score"`
		Confidence   float64 `db:"confidence"`
		ModelVersion string  `db:"model_version"`
	}
	if err := db.db.Get(&row, `
		SELECT p.score, p.confidence, p.model_version
		FROM predictions p
		JOIN symbols s ON s.id = p.symbol_id
		WHERE s.ticker = $1
		ORDER BY p.ts DESC LIMIT 1`, "TESTPRED"); err != nil {
		t.Fatalf("failed to read back prediction: %v", err)
	}
	if row.Score != 0.62 || row.Confidence != 0.72 || row.ModelVersion != "stub-v0" {
		t.Fatalf("unexpected stored prediction: %+v", row)
	}
}
