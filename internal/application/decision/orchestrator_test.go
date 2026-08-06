package decision

import (
	"math"
	"testing"
	"time"

	analyzerUC "atlas/internal/application/analyzer"
	mlUC "atlas/internal/application/ml"
	portfolioUC "atlas/internal/application/portfolio"
	domainDecision "atlas/internal/domain/decision"
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

func TestBuildTechnicalScoreUsesRealIndicatorsNotAConstant(t *testing.T) {
	orchestrator := NewOrchestrator(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), NewUseCase())

	bars := make([]market.Bar, 0, 20)
	price := 100.0
	for i := 0; i < 20; i++ {
		price += 1
		bars = append(bars, market.Bar{
			Symbol: "AAPL", Open: price - 1, High: price + 1, Low: price - 2, Close: price,
			Volume: 1000, Timestamp: time.Now().Add(time.Duration(i) * time.Minute),
		})
	}

	technical := orchestrator.buildTechnicalScore("AAPL", bars)
	if technical.RSI == 62 {
		t.Fatalf("RSI should be computed from real bars, not the old hardcoded constant")
	}
	if technical.RSI <= 50 {
		t.Fatalf("expected RSI to reflect the sustained uptrend (>50), got %v", technical.RSI)
	}
}

func TestBuildTechnicalScoreFallsBackToNeutralRSIWithShortHistory(t *testing.T) {
	orchestrator := NewOrchestrator(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), NewUseCase())
	bars := []market.Bar{
		{Symbol: "AAPL", Open: 100, High: 105, Low: 99, Close: 102, Volume: 1000, Timestamp: time.Now()},
	}

	technical := orchestrator.buildTechnicalScore("AAPL", bars)
	if technical.RSI != 50 {
		t.Fatalf("expected neutral RSI fallback of 50 with insufficient history, got %v", technical.RSI)
	}
}

func TestBlendMLAndPortfolioIsNoOpWithoutML(t *testing.T) {
	orchestrator := NewOrchestrator(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), NewUseCase())
	technical := domainDecision.TechnicalScore{Overall: 0.9}

	overall, reasons := orchestrator.blendMLAndPortfolio("AAPL", nil, technical)
	if overall != 0.9 {
		t.Fatalf("expected the blend to be a no-op without ML (plain NewOrchestrator), got %v", overall)
	}
	if len(reasons) != 0 {
		t.Fatalf("expected no extra reasons without ML, got %v", reasons)
	}
}

func TestBlendMLAndPortfolioAppliesFullMixWithML(t *testing.T) {
	orchestrator := NewOrchestratorWithML(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), NewUseCase(), mlUC.NewUseCase())
	// A single bar means RSI falls back to the neutral 50 (see BuildFeatureVector), so
	// mlScore lands on exactly 0.5 and the expected blend is precisely computable.
	bars := []market.Bar{{Symbol: "AAPL", Close: 100, Timestamp: time.Now()}}
	technical := domainDecision.TechnicalScore{Overall: 0.9}

	overall, _ := orchestrator.blendMLAndPortfolio("AAPL", bars, technical)

	want := clamp01(0.9*0.55 + 0.5*0.30 + 0.5*0.15)
	if math.Abs(overall-want) > 0.0001 {
		t.Fatalf("expected blended overall %.4f, got %.4f", want, overall)
	}
}

func TestBlendMLAndPortfolioFlagsRiskForLosingPosition(t *testing.T) {
	portfolioUseCase := portfolioUC.NewUseCase()
	portfolioUseCase.Positions = []portfolio.Position{{Symbol: "AAPL", Quantity: 10, AverageCost: 100, CurrentPrice: 85}}
	orchestrator := NewOrchestratorWithML(analyzerUC.NewUseCase(), portfolioUseCase, NewUseCase(), mlUC.NewUseCase())
	bars := []market.Bar{{Symbol: "AAPL", Close: 100, Timestamp: time.Now()}}

	_, reasons := orchestrator.blendMLAndPortfolio("AAPL", bars, domainDecision.TechnicalScore{Overall: 0.9})

	found := false
	for _, reason := range reasons {
		if reason == "Portfolio risk context reduces conviction" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a portfolio risk reason for a -15%% position, got %v", reasons)
	}
}

func TestPortfolioRiskSignal(t *testing.T) {
	positions := []portfolio.Position{
		{Symbol: "LOSS", AverageCost: 100, CurrentPrice: 85},
		{Symbol: "GAIN", AverageCost: 100, CurrentPrice: 125},
		{Symbol: "FLAT", AverageCost: 100, CurrentPrice: 102},
	}
	if got := portfolioRiskSignal("LOSS", positions); got != 0.3 {
		t.Fatalf("expected 0.3 for a -15%% position, got %v", got)
	}
	if got := portfolioRiskSignal("GAIN", positions); got != 0.4 {
		t.Fatalf("expected 0.4 for a +25%% position, got %v", got)
	}
	if got := portfolioRiskSignal("FLAT", positions); got != 0.5 {
		t.Fatalf("expected 0.5 (neutral) for a +2%% position, got %v", got)
	}
	if got := portfolioRiskSignal("NONE", positions); got != 0.5 {
		t.Fatalf("expected 0.5 (neutral) when no position exists, got %v", got)
	}
}
