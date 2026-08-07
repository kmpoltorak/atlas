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
	// ACCUMULATE, not BUY: portfolioUC already holds an AAPL position above, and a strong
	// bullish score with an existing position is exactly ACCUMULATE's differentiation from
	// BUY (see decision.UseCase.BuildRecommendation).
	if rec.Action != "ACCUMULATE" {
		t.Fatalf("expected ACCUMULATE recommendation (existing position + strong bullish score), got %q", rec.Action)
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
	if technical.OBV != 0 || technical.BollingerPercentB != 0 {
		t.Fatalf("expected OBV/BollingerPercentB to stay zero with only one bar, got OBV=%v BollingerPercentB=%v",
			technical.OBV, technical.BollingerPercentB)
	}
}

func TestBuildTechnicalScoreBlendsBollingerAndOBVWithEnoughHistory(t *testing.T) {
	orchestrator := NewOrchestrator(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), NewUseCase())

	bars := make([]market.Bar, 0, 25)
	price := 100.0
	for i := 0; i < 25; i++ {
		price += 1
		bars = append(bars, market.Bar{
			Symbol: "AAPL", Open: price - 1, High: price + 1, Low: price - 2, Close: price,
			Volume: 1000, Timestamp: time.Now().Add(time.Duration(i) * time.Minute),
		})
	}

	technical := orchestrator.buildTechnicalScore("AAPL", bars)
	if technical.BollingerPercentB <= 0.5 {
		t.Fatalf("expected %%B above the midline for a sustained uptrend (price near the upper band), got %v",
			technical.BollingerPercentB)
	}
	if technical.OBV == 0 {
		t.Fatalf("expected a non-zero OBV reading with 25 bars of rising volume-confirmed closes")
	}
	// Every contributing signal (momentum, RSI, %B, OBV) leans bullish for this sustained
	// uptrend, so the blended Overall should too.
	if technical.Overall <= 0.5 {
		t.Fatalf("expected Overall to lean bullish once Bollinger/OBV join the blend, got %v", technical.Overall)
	}
}

func TestOBVSignal(t *testing.T) {
	if got := obvSignal([]float64{100, 200, 300}); got != 0.7 {
		t.Fatalf("expected 0.7 (accumulation) when latest is above the series mean, got %v", got)
	}
	if got := obvSignal([]float64{300, 200, 100}); got != 0.3 {
		t.Fatalf("expected 0.3 (distribution) when latest is below the series mean, got %v", got)
	}
	if got := obvSignal([]float64{100, 100, 100}); got != 0.5 {
		t.Fatalf("expected 0.5 (neutral) when latest equals the series mean, got %v", got)
	}
}

func TestBlendMLAndPortfolioIsNoOpWithoutML(t *testing.T) {
	orchestrator := NewOrchestrator(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), NewUseCase())
	technical := domainDecision.TechnicalScore{Overall: 0.9}

	blend := orchestrator.blendMLAndPortfolio("AAPL", nil, technical)
	if blend.Overall != 0.9 {
		t.Fatalf("expected the blend to be a no-op without ML (plain NewOrchestrator), got %v", blend.Overall)
	}
	if len(blend.Reasons) != 0 {
		t.Fatalf("expected no extra reasons without ML, got %v", blend.Reasons)
	}
	if blend.MLConfidence != 1.0 {
		t.Fatalf("expected MLConfidence to default to 1.0 (never trigger WAIT) without ML, got %v", blend.MLConfidence)
	}
}

func TestBlendMLAndPortfolioAppliesFullMixWithML(t *testing.T) {
	orchestrator := NewOrchestratorWithML(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), NewUseCase(), mlUC.NewUseCase())
	// A single bar means RSI falls back to the neutral 50 (see BuildFeatureVector), so
	// mlScore lands on exactly 0.5 and the expected blend is precisely computable. No
	// positions means RiskScore is exactly 1.0 too (see computeRiskScore: zero drawdown, no
	// observed volatility to penalize).
	bars := []market.Bar{{Symbol: "AAPL", Close: 100, Timestamp: time.Now()}}
	technical := domainDecision.TechnicalScore{Overall: 0.9}

	blend := orchestrator.blendMLAndPortfolio("AAPL", bars, technical)

	want := clamp01(0.9*technicalWeight + 0.5*mlWeight + 0.5*portfolioWeight + 1.0*riskWeight)
	if math.Abs(blend.Overall-want) > 0.0001 {
		t.Fatalf("expected blended overall %.4f, got %.4f", want, blend.Overall)
	}
	if blend.MLScore != 0.5 {
		t.Fatalf("expected mlScore 0.5, got %v", blend.MLScore)
	}
	if blend.PortfolioScore != 0.5 {
		t.Fatalf("expected portfolioScore 0.5, got %v", blend.PortfolioScore)
	}
	if blend.RiskScore != 1.0 {
		t.Fatalf("expected riskScore 1.0 with no positions, got %v", blend.RiskScore)
	}
	if blend.MLConfidence != 0.72 {
		t.Fatalf("expected the stub's fixed 0.72 confidence, got %v", blend.MLConfidence)
	}
}

func TestComputeRiskScore(t *testing.T) {
	// No positions: no drawdown, no observed volatility to penalize — fully neutral/no-risk.
	if got := computeRiskScore(portfolio.RiskSnapshot{}, nil); got != 1.0 {
		t.Fatalf("expected 1.0 (no risk) with no positions, got %v", got)
	}

	// A losing position: -150 unrealized loss on 850 exposure (-17.6% drawdown), $15 average
	// deviation on an $85 average price (17.6% relative volatility) — both components land at
	// the same ~0.8235, so the average does too.
	positions := []portfolio.Position{{Symbol: "AAPL", Quantity: 10, AverageCost: 100, CurrentPrice: 85}}
	risk := portfolio.RiskSnapshot{Exposure: 850, Drawdown: -150.0 / 850.0, Volatility: 15}
	got := computeRiskScore(risk, positions)
	want := 1 - (150.0/850.0+15.0/85.0)/2
	if math.Abs(got-want) > 0.0001 {
		t.Fatalf("expected riskScore %.4f for a losing position, got %.4f", want, got)
	}
	if got >= 1.0 {
		t.Fatalf("expected a losing position to score below the no-risk baseline of 1.0, got %v", got)
	}
}

func TestBlendMLAndPortfolioFlagsRiskForLosingPosition(t *testing.T) {
	portfolioUseCase := portfolioUC.NewUseCase()
	portfolioUseCase.Positions = []portfolio.Position{{Symbol: "AAPL", Quantity: 10, AverageCost: 100, CurrentPrice: 85}}
	orchestrator := NewOrchestratorWithML(analyzerUC.NewUseCase(), portfolioUseCase, NewUseCase(), mlUC.NewUseCase())
	bars := []market.Bar{{Symbol: "AAPL", Close: 100, Timestamp: time.Now()}}

	blend := orchestrator.blendMLAndPortfolio("AAPL", bars, domainDecision.TechnicalScore{Overall: 0.9})

	found := false
	for _, reason := range blend.Reasons {
		if reason == "Portfolio risk context reduces conviction" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a portfolio risk reason for a -15%% position, got %v", blend.Reasons)
	}
	if !blend.HasPosition {
		t.Fatal("expected HasPosition to be true for a real, positive-quantity AAPL position")
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
