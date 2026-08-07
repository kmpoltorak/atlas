package alerts

import (
	"testing"
	"time"

	analyzerUC "atlas/internal/application/analyzer"
	collectorUC "atlas/internal/application/collector"
	decisionUC "atlas/internal/application/decision"
	portfolioUC "atlas/internal/application/portfolio"
	watchlistUC "atlas/internal/application/watchlist"
	"atlas/internal/domain/market"
)

type stubProvider struct {
	bars map[string][]market.Bar
}

func (s stubProvider) FetchBars(symbol string, window int) ([]market.Bar, error) {
	return s.bars[symbol], nil
}

func uptrendBars(symbol string) []market.Bar {
	bars := make([]market.Bar, 0, 3)
	base := time.Now()
	prices := []float64{100, 104, 109}
	for i, price := range prices {
		bars = append(bars, market.Bar{
			Symbol: symbol, Open: price - 4, High: price + 2, Low: price - 5, Close: price,
			Volume: 1200, Timestamp: base.Add(time.Duration(i) * time.Minute),
		})
	}
	return bars
}

func flatBars(symbol string) []market.Bar {
	return []market.Bar{{Symbol: symbol, Open: 100, High: 100, Low: 100, Close: 100, Volume: 100, Timestamp: time.Now()}}
}

func TestEvaluateSurfacesNonHoldAlerts(t *testing.T) {
	provider := stubProvider{bars: map[string][]market.Bar{
		"AAPL": uptrendBars("AAPL"),
		"MSFT": flatBars("MSFT"),
	}}

	watchlist := watchlistUC.NewUseCase()
	_ = watchlist.Add("AAPL", "2026-08-05T00:00:00Z")
	_ = watchlist.Add("MSFT", "2026-08-05T00:00:00Z")

	collector := collectorUC.NewUseCase(provider)
	orchestrator := decisionUC.NewOrchestrator(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), decisionUC.NewUseCase())

	uc := NewUseCase(watchlist, collector, orchestrator, 30)
	result, err := uc.Evaluate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected exactly one alert, got %d: %+v", len(result), result)
	}
	if result[0].Recommendation.Symbol != "AAPL" {
		t.Fatalf("expected alert for AAPL, got %q", result[0].Recommendation.Symbol)
	}
	if result[0].Recommendation.Action != "BUY" {
		t.Fatalf("expected BUY alert, got %q", result[0].Recommendation.Action)
	}
	if result[0].TriggeredAt == "" {
		t.Fatal("expected TriggeredAt to be set")
	}
	if len(result[0].Bars) == 0 {
		t.Fatal("expected Bars to carry the bars the recommendation was built from, for indicator-snapshot persistence")
	}
}

func TestEvaluateReturnsEmptyWhenWatchlistIsEmpty(t *testing.T) {
	watchlist := watchlistUC.NewUseCase()
	collector := collectorUC.NewUseCase(stubProvider{})
	orchestrator := decisionUC.NewOrchestrator(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), decisionUC.NewUseCase())

	uc := NewUseCase(watchlist, collector, orchestrator, 30)
	result, err := uc.Evaluate()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected no alerts, got %d", len(result))
	}
}
