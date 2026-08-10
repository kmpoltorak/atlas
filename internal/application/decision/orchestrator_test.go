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
	domainML "atlas/internal/domain/ml"
	"atlas/internal/domain/portfolio"
)

// sustainedUptrendBars is a rising series long enough for the momentum term to have a reading:
// more than momentumWindow bars of history, and per-bar returns that vary. The variation is not
// decoration — a perfectly uniform climb has zero realized volatility to normalize against, so
// momentumVolumeScore correctly reports no signal for it. Volume is held at the series average
// so the volume-confirmation factor stays at full weight and does not muddy what is being
// asserted. This replaces three-bar fixtures that only ever produced a strong score because the
// old step function read a single bar's open-to-close move.
func sustainedUptrendBars(symbol string) []market.Bar {
	bars := make([]market.Bar, 0, 30)
	base := time.Now().Add(-30 * time.Hour)
	price := 100.0
	for i := 0; i < 30; i++ {
		if i%2 == 0 {
			price *= 1.015
		} else {
			price *= 1.005
		}
		bars = append(bars, market.Bar{
			Symbol: symbol, Open: price / 1.004, High: price * 1.002, Low: price / 1.008,
			Close: price, Volume: 1200, Timestamp: base.Add(time.Duration(i) * time.Hour),
		})
	}
	return bars
}

func TestOrchestratorBuildsRecommendationFromLatestSignal(t *testing.T) {
	analyzerUC := analyzerUC.NewUseCase()
	portfolioUC := portfolioUC.NewUseCase()
	portfolioUC.Positions = []portfolio.Position{{Symbol: "AAPL", Quantity: 5, AverageCost: 100, CurrentPrice: 105}}
	decisionUC := NewUseCase()
	orchestrator := NewOrchestrator(analyzerUC, portfolioUC, decisionUC)

	rec, err := orchestrator.BuildRecommendationFromBars("AAPL", sustainedUptrendBars("AAPL"))
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

	// Technical carries the opportunity; the other three only ever scale it down. The stub's 0.5
	// is a real correction reading, so it halves; portfolio has nothing to say (0.5 is the top of
	// its scale) and risk finds nothing, so neither moves the result.
	want := clamp01(0.9 * 0.5 * 1.0 * 1.0)
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

// mlScoreFromPrediction is where recovery probability finally reaches a decision. Before this,
// recovery was computed, persisted and displayed while every recommendation ignored it.
func TestMLScoreFoldsInRecoveryProbability(t *testing.T) {
	const modelVersion = "v-test"

	// Same 60% chance of a drop, but one is expected to bounce back and the other isn't. The
	// recoverable one must score as the safer of the two.
	recoverable := domainML.Prediction{
		ModelVersion: modelVersion, CorrectionProbability: 0.6, RecoveryProbability: 0.9,
	}
	permanent := domainML.Prediction{
		ModelVersion: modelVersion, CorrectionProbability: 0.6, RecoveryProbability: 0.0,
	}
	if got, want := mlScoreFromPrediction(recoverable), mlScoreFromPrediction(permanent); got <= want {
		t.Errorf("a drop expected to recover must score higher than one that isn't: %v vs %v", got, want)
	}

	// P(unrecovered drop) = 0.6 * (1-0.9) = 0.06, so the term is 0.94.
	if got := mlScoreFromPrediction(recoverable); math.Abs(got-0.94) > 1e-9 {
		t.Errorf("expected 1 - 0.6*(1-0.9) = 0.94, got %v", got)
	}

	// With no recovery model loaded RecoveryProbability is 0, which must reproduce the previous
	// 1 - P(correction) behaviour exactly rather than silently shifting every recommendation.
	noRecovery := domainML.Prediction{
		ModelVersion: modelVersion, CorrectionProbability: 0.25, RecoveryProbability: 0,
	}
	if got := mlScoreFromPrediction(noRecovery); math.Abs(got-0.75) > 1e-9 {
		t.Errorf("without a recovery model the term must stay 1 - P(correction) = 0.75, got %v", got)
	}
}

// The stub has no correction-probability concept, so running its RSI-derived placeholder through
// a risk formula would be inventing meaning it doesn't carry.
func TestMLScoreLeavesTheStubUntouched(t *testing.T) {
	stub := domainML.Prediction{ModelVersion: domainML.StubModelVersion, Score: 0.62}
	if got := mlScoreFromPrediction(stub); got != 0.62 {
		t.Errorf("stub Score must pass through unchanged, got %v", got)
	}
}

// momentumVolumeScore reports "no information" as a neutral 0.5 rather than a fabricated lean
// whenever its inputs cannot support a reading. Each of these cases was previously served by a
// step function that returned a confident-looking 0.48 regardless.
func TestMomentumVolumeScoreIsNeutralWithoutUsableInput(t *testing.T) {
	analyzer := analyzerUC.NewUseCase()
	bars := sustainedUptrendBars("AAPL")

	if got := momentumVolumeScore(nil, "AAPL", bars); got != 0.5 {
		t.Errorf("without an analyzer the term must stay neutral, got %v", got)
	}
	if got := momentumVolumeScore(analyzer, "AAPL", bars[:momentumWindow]); got != 0.5 {
		t.Errorf("with only %d bars there is no window to measure over, got %v", momentumWindow, got)
	}

	// A perfectly flat series has no realized volatility to normalize the move against, so the
	// ratio is undefined rather than infinitely significant.
	flat := make([]market.Bar, 0, 30)
	for i := 0; i < 30; i++ {
		flat = append(flat, market.Bar{
			Symbol: "AAPL", Open: 100, High: 100, Low: 100, Close: 100, Volume: 1000,
			Timestamp: time.Now().Add(time.Duration(i) * time.Hour),
		})
	}
	if got := momentumVolumeScore(analyzer, "AAPL", flat); got != 0.5 {
		t.Errorf("a flat series has no measurable volatility, so the term must stay neutral, got %v", got)
	}
}

// The term must be directional and stay inside [0,1] at both extremes — the old step function
// was neither, quantizing every reading to 0.82 or 0.48.
func TestMomentumVolumeScoreIsDirectionalAndBounded(t *testing.T) {
	analyzer := analyzerUC.NewUseCase()

	up := momentumVolumeScore(analyzer, "AAPL", sustainedUptrendBars("AAPL"))
	if up <= 0.5 || up > 1 {
		t.Fatalf("a sustained uptrend must read bullish within [0,1], got %v", up)
	}

	// The same series reversed in price: an equally sustained decline, which must read bearish.
	rising := sustainedUptrendBars("AAPL")
	falling := make([]market.Bar, len(rising))
	for i, bar := range rising {
		mirror := rising[len(rising)-1-i]
		falling[i] = market.Bar{
			Symbol: bar.Symbol, Open: mirror.Open, High: mirror.High, Low: mirror.Low,
			Close: mirror.Close, Volume: bar.Volume, Timestamp: bar.Timestamp,
		}
	}
	down := momentumVolumeScore(analyzer, "AAPL", falling)
	if down >= 0.5 || down < 0 {
		t.Fatalf("a sustained decline must read bearish within [0,1], got %v", down)
	}
}

// Thin volume damps the reading toward neutral rather than reversing it: a move nobody traded is
// weak evidence, not counter-evidence.
func TestMomentumVolumeScoreDampsUnconfirmedMoves(t *testing.T) {
	analyzer := analyzerUC.NewUseCase()

	confirmed := sustainedUptrendBars("AAPL")
	thin := sustainedUptrendBars("AAPL")
	thin[len(thin)-1].Volume = 0

	confirmedScore := momentumVolumeScore(analyzer, "AAPL", confirmed)
	thinScore := momentumVolumeScore(analyzer, "AAPL", thin)

	if thinScore >= confirmedScore {
		t.Fatalf("an unconfirmed move must score lower than a confirmed one, got %v vs %v", thinScore, confirmedScore)
	}
	if thinScore <= 0.5 {
		t.Fatalf("damping must pull toward neutral, not past it, got %v", thinScore)
	}
}
