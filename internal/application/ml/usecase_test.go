package ml

import (
	"math"
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
	// A sustained uptrend must leave the close above its own trailing EMA.
	if feature.EMADist <= 0 {
		t.Fatalf("expected a positive close-vs-EMA distance in an uptrend, got %v", feature.EMADist)
	}
	if feature.TRPct <= 0 {
		t.Fatalf("expected a positive true-range fraction, got %v", feature.TRPct)
	}
}

// TestBuildFeatureVectorIsScaleFree is the regression test for the modelling bug documented on
// domain/ml.FeatureVector: two symbols with identical *shape* but wildly different price levels
// must produce (near-)identical features. Before normalisation, EMA/MACD/ATR carried the raw
// price level, so this pair differed by a factor of 1000 and any tree split on them was really a
// symbol lookup.
func TestBuildFeatureVectorIsScaleFree(t *testing.T) {
	build := func(base float64) ml.FeatureVector {
		bars := make([]market.Bar, 0, 20)
		price := base
		for i := 0; i < 20; i++ {
			price *= 1.01
			bars = append(bars, market.Bar{
				Symbol: "X", Open: price * 0.99, High: price * 1.01, Low: price * 0.98, Close: price,
				Volume: 1000, Timestamp: time.Now().Add(time.Duration(i) * time.Minute),
			})
		}
		return BuildFeatureVector(analyzerUC.NewUseCase(), "X", bars)
	}

	cheap, expensive := build(10), build(10000)

	const tolerance = 1e-9
	if math.Abs(cheap.EMADist-expensive.EMADist) > tolerance {
		t.Errorf("EMADist is price-scale dependent: %v vs %v", cheap.EMADist, expensive.EMADist)
	}
	if math.Abs(cheap.MACDPct-expensive.MACDPct) > tolerance {
		t.Errorf("MACDPct is price-scale dependent: %v vs %v", cheap.MACDPct, expensive.MACDPct)
	}
	if math.Abs(cheap.TRPct-expensive.TRPct) > tolerance {
		t.Errorf("TRPct is price-scale dependent: %v vs %v", cheap.TRPct, expensive.TRPct)
	}
	if math.Abs(cheap.RSI-expensive.RSI) > tolerance {
		t.Errorf("RSI is price-scale dependent: %v vs %v", cheap.RSI, expensive.RSI)
	}
}

// TestBuildFeatureVectorSurvivesNonPositiveClose guards the division-by-zero path: a bad bar
// should degrade to "no signal", never to an Inf/NaN the ONNX model would silently consume.
func TestBuildFeatureVectorSurvivesNonPositiveClose(t *testing.T) {
	bars := []market.Bar{
		{Symbol: "X", Open: 0, High: 0, Low: 0, Close: 0, Volume: 0, Timestamp: time.Now()},
		{Symbol: "X", Open: 0, High: 0, Low: 0, Close: 0, Volume: 0, Timestamp: time.Now().Add(time.Minute)},
	}
	feature := BuildFeatureVector(analyzerUC.NewUseCase(), "X", bars)
	for name, value := range map[string]float64{
		"RSI": feature.RSI, "EMADist": feature.EMADist, "MACDPct": feature.MACDPct,
		"TRPct": feature.TRPct, "Volume": feature.Volume,
	} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Errorf("%s is not finite: %v", name, value)
		}
	}
}
