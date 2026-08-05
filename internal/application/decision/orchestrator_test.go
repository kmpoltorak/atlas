package decision

import (
	"testing"
	"time"

	analyzerUC "atlas/internal/application/analyzer"
	portfolioUC "atlas/internal/application/portfolio"
	"atlas/internal/domain/market"
	"atlas/internal/domain/portfolio"
)

func TestOrchestratorBuildsRecommendationFromLatestSignal(t *testing.T) {
	analyzerUC := analyzerUC.NewUseCase()
	portfolioUC := portfolioUC.NewUseCase()
	portfolioUC.Positions = []portfolio.Position{{Symbol: "AAPL", Quantity: 5, AverageCost: 100, CurrentPrice: 105}}
	decisionUC := NewUseCase()
	orchestrator := NewOrchestrator(analyzerUC, portfolioUC, decisionUC)

	bars := []market.Bar{
		{Symbol: "AAPL", Open: 100, High: 105, Low: 99, Close: 102, Volume: 1000, Timestamp: time.Now().Add(-2 * time.Minute)},
		{Symbol: "AAPL", Open: 102, High: 106, Low: 101, Close: 104, Volume: 1100, Timestamp: time.Now().Add(-1 * time.Minute)},
		{Symbol: "AAPL", Open: 104, High: 108, Low: 103, Close: 107, Volume: 1200, Timestamp: time.Now()},
	}

	rec, err := orchestrator.BuildRecommendationFromBars("AAPL", bars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Symbol != "AAPL" {
		t.Fatalf("expected symbol AAPL, got %q", rec.Symbol)
	}
	if rec.Action != "BUY" {
		t.Fatalf("expected BUY recommendation, got %q", rec.Action)
	}
}

func TestOrchestratorRejectsEmptyBarSet(t *testing.T) {
	orchestrator := NewOrchestrator(nil, nil, NewUseCase())
	rec, err := orchestrator.BuildRecommendationFromBars("AAPL", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Action != "HOLD" {
		t.Fatalf("expected HOLD recommendation, got %q", rec.Action)
	}
}
