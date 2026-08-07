package decision

import (
	"math"

	analyzerUC "atlas/internal/application/analyzer"
	mlUC "atlas/internal/application/ml"
	portfolioUC "atlas/internal/application/portfolio"
	domainDecision "atlas/internal/domain/decision"
	"atlas/internal/domain/market"
	"atlas/internal/domain/portfolio"
)

// EngineVersion tags every Recommendation this orchestrator produces (Recommendation.
// ModelVersion), independent of ML's own model_version (see ml.Prediction) — it identifies the
// decision-engine scoring logic (weights, thresholds), not the ML sub-model.
const EngineVersion = "atlas-decision-v1"

// Orchestrator composes the analyzer, ML, portfolio, and decision use cases into an explainable
// recommendation workflow, per docs/decision_engine.md's "Scoring Model" (Technical + ML +
// Portfolio + Risk). ML is optional (nil-safe) since most callers/tests only care about the
// technical path; use NewOrchestratorWithML to wire the full blend.
type Orchestrator struct {
	Analyzer  *analyzerUC.UseCase
	Portfolio *portfolioUC.UseCase
	Decision  *UseCase
	ML        *mlUC.UseCase
}

func NewOrchestrator(analyzerUC *analyzerUC.UseCase, portfolioUC *portfolioUC.UseCase, decisionUC *UseCase) *Orchestrator {
	return &Orchestrator{
		Analyzer:  analyzerUC,
		Portfolio: portfolioUC,
		Decision:  decisionUC,
	}
}

// NewOrchestratorWithML wires the ML use case in as well, enabling the full Technical+ML+
// Portfolio blend in buildTechnicalScore. Kept separate from NewOrchestrator so existing
// technical-only call sites and tests don't need an ML use case they don't care about.
func NewOrchestratorWithML(analyzerUC *analyzerUC.UseCase, portfolioUC *portfolioUC.UseCase, decisionUC *UseCase, mlUC *mlUC.UseCase) *Orchestrator {
	o := NewOrchestrator(analyzerUC, portfolioUC, decisionUC)
	o.ML = mlUC
	return o
}

// BuildRecommendationFromBars builds a recommendation from OHLCV bars, a portfolio snapshot, and the decision engine.
func (o *Orchestrator) BuildRecommendationFromBars(symbol string, bars []market.Bar) (domainDecision.Recommendation, error) {
	if len(bars) == 0 {
		return domainDecision.Recommendation{Symbol: symbol, Action: "HOLD", Confidence: 0.5, Reason: []string{"No signal data available"}}, nil
	}
	if o.Decision == nil {
		return domainDecision.Recommendation{Symbol: symbol, Action: "HOLD", Confidence: 0.5, Reason: []string{"Decision engine unavailable"}}, nil
	}
	technical := o.buildTechnicalScore(symbol, bars)
	technicalScore := technical.Overall
	blend := o.blendMLAndPortfolio(symbol, bars, technical)
	technical.Overall = blend.Overall

	recommendation, err := o.Decision.BuildRecommendation(domainDecision.RecommendationInput{
		Technical:    technical,
		HasPosition:  blend.HasPosition,
		MLConfidence: blend.MLConfidence,
	})
	if err != nil {
		return domainDecision.Recommendation{}, err
	}
	recommendation.Symbol = symbol
	recommendation.Reason = append(recommendation.Reason, blend.Reasons...)
	recommendation.Score = technical.Overall
	recommendation.TechnicalScore = technicalScore
	recommendation.MLScore = blend.MLScore
	recommendation.PortfolioScore = blend.PortfolioScore
	recommendation.RiskScore = blend.RiskScore
	recommendation.MarketCondition = mlUC.ClassifyMarketCondition(o.Analyzer, symbol, bars)
	recommendation.ModelVersion = EngineVersion
	return recommendation, nil
}

// BuildRecommendationForSymbol wraps the signal pipeline and returns an explainable recommendation object.
func (o *Orchestrator) BuildRecommendationForSymbol(symbol string, signals []domainDecision.TechnicalScore) (domainDecision.Recommendation, error) {
	if len(signals) == 0 {
		return domainDecision.Recommendation{Symbol: symbol, Action: "HOLD", Confidence: 0.5, Reason: []string{"No signal data available"}}, nil
	}
	latest := signals[len(signals)-1]
	if o.Decision == nil {
		return domainDecision.Recommendation{Symbol: symbol, Action: "HOLD", Confidence: 0.5, Reason: []string{"Decision engine unavailable"}}, nil
	}
	// No bars/portfolio/ML context available from a bare signal slice — defaults preserve the
	// original BUY/SELL/HOLD-only behavior (no known position, confidence high enough to never
	// trigger WAIT). See BuildRecommendationFromBars for the full blend.
	recommendation, err := o.Decision.BuildRecommendation(domainDecision.RecommendationInput{
		Technical: latest, HasPosition: false, MLConfidence: 1.0,
	})
	if err != nil {
		return domainDecision.Recommendation{}, err
	}
	recommendation.Symbol = symbol
	return recommendation, nil
}

// buildTechnicalScore derives an explainable TechnicalScore from the real analyzer indicators.
// RSI falls back to a neutral baseline only when there isn't enough history (< 15 bars) to
// compute Wilder's RSI; every other field reflects the actual indicator series.
func (o *Orchestrator) buildTechnicalScore(symbol string, bars []market.Bar) domainDecision.TechnicalScore {
	latest := bars[len(bars)-1]
	momentumVolume := overallScore(latest.Close, latest.Open, latest.Volume)

	technical := domainDecision.TechnicalScore{RSI: 50, EMA: latest.Close, Overall: momentumVolume}
	if o.Analyzer == nil {
		return technical
	}

	if series, err := o.Analyzer.ComputeRSI(symbol, bars); err == nil && len(series) > 0 {
		technical.RSI = series[len(series)-1]
	}
	if series, err := o.Analyzer.ComputeEMA(symbol, bars); err == nil && len(series) > 0 {
		technical.EMA = series[len(series)-1]
	}
	if series, err := o.Analyzer.ComputeMACD(symbol, bars); err == nil && len(series) > 0 {
		technical.MACD = series[len(series)-1]
	}
	if series, err := o.Analyzer.ComputeATR(symbol, bars); err == nil && len(series) > 0 {
		technical.ATR = series[len(series)-1]
	}
	if series, err := o.Analyzer.ComputeVolume(symbol, bars); err == nil && len(series) > 0 {
		technical.Volume = series[len(series)-1]
	}

	// Blend every signal that was actually computed into Overall as a weighted average —
	// momentum/volume always contributes (it's the only signal that needs no warm-up period);
	// RSI/Bollinger/OBV each join only once there's enough history to compute them (matching
	// the RSI-only guard this replaced). With only momentum/volume present, the weighted
	// average of one term reduces to momentumVolume unchanged, so short-history behavior is
	// identical to before this pass. Weights (0.45/0.20/0.20/0.15) are first-pass, documented
	// heuristics, same honesty stance as this package's other hand-picked cutoffs (see
	// decision.DefaultThresholds) — not tuned against real outcome data.
	terms := []struct{ value, weight float64 }{{momentumVolume, 0.45}}

	if technical.RSI != 50 {
		rsiSignal := 0.5 + (technical.RSI-50)/100
		terms = append(terms, struct{ value, weight float64 }{rsiSignal, 0.20})
	}

	// %B, the standard Bollinger Bands position measure: price stretched toward the upper band
	// reads as trend strength here (bullish), matching this engine's existing trend-following
	// stance (see the RSI treatment above) rather than a mean-reversion "overbought = sell" one.
	if bands, err := o.Analyzer.ComputeBollingerBands(symbol, bars); err == nil && len(bands.Upper) > 0 {
		i := len(bands.Upper) - 1
		if upper, lower := bands.Upper[i], bands.Lower[i]; upper > lower {
			technical.BollingerPercentB = clamp01((latest.Close - lower) / (upper - lower))
			terms = append(terms, struct{ value, weight float64 }{technical.BollingerPercentB, 0.20})
		}
	}

	// OBV confirmation: a coarse, standard use of On-Balance Volume — is the latest reading
	// above or below its own recent average, i.e. is volume flow presently accumulating or
	// distributing. This is deliberately coarse (not divergence detection against price, which
	// would need a more involved analysis) — see docs/decision_engine.md.
	if series, err := o.Analyzer.ComputeOBV(symbol, bars); err == nil && len(series) >= 2 {
		technical.OBV = series[len(series)-1]
		terms = append(terms, struct{ value, weight float64 }{obvSignal(series), 0.15})
	}

	sumWeight, sumValue := 0.0, 0.0
	for _, term := range terms {
		sumWeight += term.weight
		sumValue += term.value * term.weight
	}
	if sumWeight > 0 {
		technical.Overall = clamp01(sumValue / sumWeight)
	}

	return technical
}

// obvSignal reports whether On-Balance Volume is presently above (0.7, accumulation-leaning) or
// below (0.3, distribution-leaning) its own mean over the given series, or exactly at it (0.5).
// Coarse by design — see buildTechnicalScore's OBV comment.
func obvSignal(series []float64) float64 {
	sum := 0.0
	for _, v := range series {
		sum += v
	}
	mean := sum / float64(len(series))
	latest := series[len(series)-1]
	switch {
	case latest > mean:
		return 0.7
	case latest < mean:
		return 0.3
	default:
		return 0.5
	}
}

// blendResult carries every signal blendMLAndPortfolio derives: the blended score, its raw
// components (for the persisted score breakdown — see Recommendation.MLScore/PortfolioScore/
// RiskScore), and the extra context (HasPosition, MLConfidence) BuildRecommendationFromBars
// needs to build a decision.RecommendationInput for ACCUMULATE/REDUCE/WAIT differentiation.
type blendResult struct {
	Overall        float64
	MLScore        float64
	PortfolioScore float64
	RiskScore      float64
	MLConfidence   float64
	HasPosition    bool
	Reasons        []string
}

// blendWeights are the first-pass, documented weights for Technical+ML+Portfolio+Risk (see
// docs/decision_engine.md's "Scoring Model"), same honesty stance as every other threshold in
// this engine — picked by hand, pending real tuning, not derived from data. Sums to 1.
const (
	technicalWeight = 0.50
	mlWeight        = 0.25
	portfolioWeight = 0.15
	riskWeight      = 0.10
)

// blendMLAndPortfolio folds the ML prediction, per-symbol portfolio P&L, and portfolio-wide
// risk context into the technical score, per docs/decision_engine.md's Technical+ML+Portfolio+
// Risk scoring model. All three are optional (nil-safe) and neutral when unavailable, so the
// blend degrades to technical-only exactly as before when ML/Portfolio aren't wired in (see
// NewOrchestrator vs NewOrchestratorWithML). MLConfidence defaults to 1.0 (never triggers
// decision.UseCase's WAIT band) when ML isn't wired in — there's no real confidence signal to
// distrust in that mode.
func (o *Orchestrator) blendMLAndPortfolio(symbol string, bars []market.Bar, technical domainDecision.TechnicalScore) blendResult {
	result := blendResult{Overall: technical.Overall, MLScore: 0.5, PortfolioScore: 0.5, RiskScore: 0.5, MLConfidence: 1.0}

	if o.ML != nil {
		feature := mlUC.BuildFeatureVector(o.Analyzer, symbol, bars)
		if prediction, err := o.ML.Predict(feature); err == nil {
			result.MLScore = prediction.Score
			result.MLConfidence = prediction.Confidence
		}
	}

	if o.Portfolio != nil {
		if positions, err := o.Portfolio.GetPositions(); err == nil {
			result.PortfolioScore = portfolioRiskSignal(symbol, positions)
			result.HasPosition = hasPosition(symbol, positions)
		}
		if risk, err := o.Portfolio.ComputeRisk(); err == nil {
			positions, _ := o.Portfolio.GetPositions()
			result.RiskScore = computeRiskScore(risk, positions)
		}
	}

	// Gated on ML specifically (not "ML or Portfolio"): every existing NewOrchestrator call
	// site already passes a non-nil Portfolio, so gating on either would have silently
	// changed behavior everywhere instead of only where NewOrchestratorWithML opts in.
	// Portfolio's non-nil-ness on its own does not enable the blend.
	if o.ML != nil {
		// Technical stays dominant; ML/Portfolio/Risk are secondary signals. ML is currently
		// RSI-derived too (see ml.UseCase.Predict) when no trained model is loaded, so this is
		// partly redundant with the RSI blend in buildTechnicalScore until a real model exists —
		// wiring the composition now means Atlas picks up a genuine ML signal automatically the
		// moment Predict() stops being a stub, with no further orchestration changes.
		result.Overall = clamp01(technical.Overall*technicalWeight + result.MLScore*mlWeight +
			result.PortfolioScore*portfolioWeight + result.RiskScore*riskWeight)
	}

	if result.MLScore >= 0.6 {
		result.Reasons = append(result.Reasons, "ML signal supportive")
	} else if result.MLScore <= 0.4 {
		result.Reasons = append(result.Reasons, "ML signal cautious")
	}
	if result.PortfolioScore < 0.5 {
		result.Reasons = append(result.Reasons, "Portfolio risk context reduces conviction")
	}
	if result.RiskScore < 0.5 {
		result.Reasons = append(result.Reasons, "Elevated portfolio-wide risk (drawdown/volatility)")
	}

	return result
}

// computeRiskScore converts a portfolio-wide risk snapshot (portfolio.UseCase.ComputeRisk) into
// a [0,1] signal on the same scale as the other blend components (higher = more supportive of
// buying, lower = more cautionary) — unlike portfolioRiskSignal (a single symbol's own P&L),
// this reflects the whole portfolio's exposure, independent of which symbol is being scored,
// matching docs/decision_engine.md's "Risk Context" being a portfolio-wide concept distinct
// from the per-position "Portfolio Context". First-pass documented heuristic, same honesty
// stance as every other threshold/weight in this engine.
func computeRiskScore(risk portfolio.RiskSnapshot, positions []portfolio.Position) float64 {
	// Drawdown is already a ratio in (-inf, 0] (see ComputeRisk) — 0 means no unrealized losses
	// across the portfolio, more negative means larger relative losses.
	drawdownComponent := clamp01(1 + risk.Drawdown)

	// Volatility is raw currency units (average abs price deviation across positions), not a
	// ratio — normalize against the average current price so it's comparable across symbols at
	// different price scales, rather than unfairly penalizing a highly-priced stock's larger
	// absolute moves. No positions means no observed volatility to penalize, so it stays neutral.
	volatilityComponent := 1.0
	if avgPrice := averageCurrentPrice(positions); avgPrice > 0 {
		volatilityComponent = clamp01(1 - risk.Volatility/avgPrice)
	}

	return clamp01((drawdownComponent + volatilityComponent) / 2)
}

func averageCurrentPrice(positions []portfolio.Position) float64 {
	if len(positions) == 0 {
		return 0
	}
	sum := 0.0
	for _, position := range positions {
		sum += position.CurrentPrice
	}
	return sum / float64(len(positions))
}

// hasPosition reports whether the portfolio holds a real (positive-quantity) position in
// symbol — used to gate BUY/ACCUMULATE and SELL/REDUCE, independent of that position's P&L
// (see portfolioRiskSignal, which reads P&L for a different purpose: risk-adjusting the score).
func hasPosition(symbol string, positions []portfolio.Position) bool {
	for _, position := range positions {
		if position.Symbol == symbol && position.Quantity > 0 {
			return true
		}
	}
	return false
}

// portfolioRiskSignal is a placeholder heuristic: a large unrealized loss is a risk signal
// (bias away from adding to a losing position), a large unrealized gain is a profit-taking
// signal (bias away from chasing further). No position, or a middling P&L, is neutral.
func portfolioRiskSignal(symbol string, positions []portfolio.Position) float64 {
	for _, position := range positions {
		if position.Symbol != symbol || position.AverageCost <= 0 {
			continue
		}
		roi := (position.CurrentPrice - position.AverageCost) / position.AverageCost
		if roi <= -0.10 {
			return 0.3
		}
		if roi >= 0.20 {
			return 0.4
		}
		return 0.5
	}
	return 0.5
}

// overallScore is a placeholder momentum/volume heuristic pending real threshold tuning.
// volume/1000 assumes typical daily-bar equity share-volume scale; feeding differently-scaled
// volume data (e.g. dollar volume, or a thin instrument) would silently skew this.
func overallScore(close, open, volume float64) float64 {
	momentum := (close - open) / math.Max(1, open)
	volumeScore := volume / 1000
	if momentum > 0.02 && volumeScore > 1 {
		return 0.82
	}
	return 0.48
}

func clamp01(value float64) float64 {
	return math.Min(1, math.Max(0, value))
}
