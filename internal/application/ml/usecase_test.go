package ml

import (
	"testing"

	"atlas/internal/domain/ml"
)

func TestBuildTrainingDatasetReturnsDeterministicRows(t *testing.T) {
	uc := NewUseCase()
	rows, err := uc.BuildTrainingDataset()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected two rows, got %d", len(rows))
	}
}

func TestPredictReturnsStablePrediction(t *testing.T) {
	uc := NewUseCase()
	pred, err := uc.Predict(ml.FeatureVector{RSI: 62})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pred.Confidence != 0.72 {
		t.Fatalf("expected confidence 0.72, got %v", pred.Confidence)
	}
}
