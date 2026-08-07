package persistence

import (
	"database/sql"
	"testing"

	"atlas/internal/domain/ml"
)

type storedPrediction struct {
	Score                 float64         `db:"score"`
	Confidence            float64         `db:"confidence"`
	ModelVersion          string          `db:"model_version"`
	CorrectionProbability sql.NullFloat64 `db:"correction_probability"`
	RecoveryProbability   sql.NullFloat64 `db:"recovery_probability"`
	RecoveryModelVersion  sql.NullString  `db:"recovery_model_version"`
}

func readBackPrediction(t *testing.T, db *TimescaleDB, ticker string) storedPrediction {
	t.Helper()
	var row storedPrediction
	if err := db.db.Get(&row, `
		SELECT p.score, p.confidence, p.model_version, p.correction_probability,
		       p.recovery_probability, p.recovery_model_version
		FROM predictions p
		JOIN symbols s ON s.id = p.symbol_id
		WHERE s.ticker = $1
		ORDER BY p.ts DESC LIMIT 1`, ticker); err != nil {
		t.Fatalf("failed to read back prediction: %v", err)
	}
	return row
}

func TestSavePredictionInsertsRecord(t *testing.T) {
	db := connectTestDB(t)

	pred := ml.Prediction{Symbol: "TESTPRED", Score: 0.62, Confidence: 0.72, ModelVersion: ml.StubModelVersion}
	if err := db.SavePrediction(pred); err != nil {
		t.Fatalf("SavePrediction failed: %v", err)
	}

	row := readBackPrediction(t, db, "TESTPRED")
	if row.Score != 0.62 || row.Confidence != 0.72 || row.ModelVersion != ml.StubModelVersion {
		t.Fatalf("unexpected stored prediction: %+v", row)
	}
	if row.CorrectionProbability.Valid {
		t.Fatalf("expected correction_probability to stay NULL for the stub model, got %v", row.CorrectionProbability.Float64)
	}
	if row.RecoveryProbability.Valid || row.RecoveryModelVersion.Valid {
		t.Fatalf("expected recovery_probability/recovery_model_version to stay NULL without a recovery model, got %+v / %+v", row.RecoveryProbability, row.RecoveryModelVersion)
	}
}

func TestSavePredictionWritesCorrectionProbabilityForRealModel(t *testing.T) {
	db := connectTestDB(t)

	pred := ml.Prediction{
		Symbol: "TESTPREDREAL", Score: 0.7, Confidence: 0.4,
		CorrectionProbability: 0.3, ModelVersion: "atlas-ml-v1",
	}
	if err := db.SavePrediction(pred); err != nil {
		t.Fatalf("SavePrediction failed: %v", err)
	}

	row := readBackPrediction(t, db, "TESTPREDREAL")
	if !row.CorrectionProbability.Valid {
		t.Fatal("expected correction_probability to be written for a real model")
	}
	if row.CorrectionProbability.Float64 != 0.3 {
		t.Fatalf("expected correction_probability 0.3, got %v", row.CorrectionProbability.Float64)
	}
	if row.RecoveryProbability.Valid || row.RecoveryModelVersion.Valid {
		t.Fatalf("expected recovery_probability/recovery_model_version to stay NULL without a recovery model, got %+v / %+v", row.RecoveryProbability, row.RecoveryModelVersion)
	}
}

func TestSavePredictionWritesRecoveryProbabilityForRealRecoveryModel(t *testing.T) {
	db := connectTestDB(t)

	pred := ml.Prediction{
		Symbol: "TESTPREDRECOVERY", Score: 0.7, Confidence: 0.4,
		CorrectionProbability: 0.3, ModelVersion: "atlas-ml-v1",
		RecoveryProbability: 0.55, RecoveryModelVersion: "atlas-ml-recovery-v1",
	}
	if err := db.SavePrediction(pred); err != nil {
		t.Fatalf("SavePrediction failed: %v", err)
	}

	row := readBackPrediction(t, db, "TESTPREDRECOVERY")
	if !row.RecoveryProbability.Valid || row.RecoveryProbability.Float64 != 0.55 {
		t.Fatalf("expected recovery_probability 0.55, got %+v", row.RecoveryProbability)
	}
	if !row.RecoveryModelVersion.Valid || row.RecoveryModelVersion.String != "atlas-ml-recovery-v1" {
		t.Fatalf("expected recovery_model_version %q, got %+v", "atlas-ml-recovery-v1", row.RecoveryModelVersion)
	}
}
