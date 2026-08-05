package ml

import (
	"testing"
	"time"

	analyzerUC "atlas/internal/application/analyzer"
	"atlas/internal/domain/market"
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

func TestBuildFeatureVectorFallsBackWithoutAnalyzerOrBars(t *testing.T) {
	feature := BuildFeatureVector(nil, "AAPL", nil)
	if feature.RSI != 50 {
		t.Fatalf("expected neutral RSI fallback, got %v", feature.RSI)
	}
}

func TestBuildFeatureVectorUsesRealIndicators(t *testing.T) {
	bars := make([]market.Bar, 0, 20)
	price := 100.0
	for i := 0; i < 20; i++ {
		price += 1
		bars = append(bars, market.Bar{
			Symbol: "AAPL", Open: price - 1, High: price + 1, Low: price - 2, Close: price,
			Volume: 1000, Timestamp: time.Now().Add(time.Duration(i) * time.Minute),
		})
	}

	feature := BuildFeatureVector(analyzerUC.NewUseCase(), "AAPL", bars)
	if feature.RSI == 50 {
		t.Fatal("expected RSI to be computed from the sustained uptrend, not the neutral fallback")
	}
	if feature.EMA == 0 {
		t.Fatal("expected EMA to be computed")
	}
}
