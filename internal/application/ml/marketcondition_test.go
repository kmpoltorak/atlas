package ml

import (
	"math"
	"testing"
	"time"

	analyzerUC "atlas/internal/application/analyzer"
	"atlas/internal/domain/market"
)

func trendingBars(n int, drift float64) []market.Bar {
	bars := make([]market.Bar, 0, n)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	price := 100.0
	for i := 0; i < n; i++ {
		price += drift
		bars = append(bars, market.Bar{
			Symbol: "AAPL", Open: price - 1, High: price + 2, Low: price - 2, Close: price,
			Volume: 1000, Timestamp: base.AddDate(0, 0, i),
		})
	}
	return bars
}

func choppyBars(n int) []market.Bar {
	bars := make([]market.Bar, 0, n)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		// Oscillates within a tight, unchanging band — no sustained directional move.
		price := 100 + math.Mod(float64(i), 2)
		bars = append(bars, market.Bar{
			Symbol: "AAPL", Open: price, High: price + 0.3, Low: price - 0.3, Close: price,
			Volume: 1000, Timestamp: base.AddDate(0, 0, i),
		})
	}
	return bars
}

func TestClassifyMarketConditionDetectsBullTrend(t *testing.T) {
	got := ClassifyMarketCondition(analyzerUC.NewUseCase(), "AAPL", trendingBars(40, 1.5))
	if got != MarketConditionBull {
		t.Fatalf("expected BULL for a sustained uptrend, got %q", got)
	}
}

func TestClassifyMarketConditionDetectsBearTrend(t *testing.T) {
	got := ClassifyMarketCondition(analyzerUC.NewUseCase(), "AAPL", trendingBars(40, -1.5))
	if got != MarketConditionBear {
		t.Fatalf("expected BEAR for a sustained downtrend, got %q", got)
	}
}

func TestClassifyMarketConditionDetectsSideways(t *testing.T) {
	got := ClassifyMarketCondition(analyzerUC.NewUseCase(), "AAPL", choppyBars(40))
	if got != MarketConditionSideways {
		t.Fatalf("expected SIDEWAYS for a flat, choppy range, got %q", got)
	}
}

func TestClassifyMarketConditionReturnsEmptyWithInsufficientHistory(t *testing.T) {
	got := ClassifyMarketCondition(analyzerUC.NewUseCase(), "AAPL", trendingBars(10, 1))
	if got != "" {
		t.Fatalf("expected empty string with too little history for ADX's warm-up, got %q", got)
	}
}

func TestClassifyMarketConditionReturnsEmptyWithoutAnalyzer(t *testing.T) {
	if got := ClassifyMarketCondition(nil, "AAPL", trendingBars(40, 1)); got != "" {
		t.Fatalf("expected empty string with a nil analyzer, got %q", got)
	}
}
