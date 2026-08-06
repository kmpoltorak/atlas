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
	var mlScore, portfolioScore float64
	var extraReasons []string
	technical.Overall, mlScore, portfolioScore, extraReasons = o.blendMLAndPortfolio(symbol, bars, technical)
	recommendation, err := o.Decision.BuildRecommendation(technical)
	if err != nil {
		return domainDecision.Recommendation{}, err
	}
	recommendation.Symbol = symbol
	recommendation.Reason = append(recommendation.Reason, extraReasons...)
	recommendation.Score = technical.Overall
	recommendation.TechnicalScore = technicalScore
	recommendation.MLScore = mlScore
	recommendation.PortfolioScore = portfolioScore
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
	recommendation, err := o.Decision.BuildRecommendation(latest)
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

	// Blend the real RSI into the overall score only when it was actually computed;
	// otherwise keep the momentum/volume score untouched. Momentum/volume is weighted
	// higher (0.7) than RSI (0.3): RSI reflects recent overbought/oversold state, not
	// trend direction, so it's a secondary signal here rather than an equal partner.
	if technical.RSI != 50 {
		rsiSignal := 0.5 + (technical.RSI-50)/100
		technical.Overall = clamp01(momentumVolume*0.7 + rsiSignal*0.3)
	}

	return technical
}

// blendMLAndPortfolio folds the ML prediction and portfolio risk context into the technical
// score, per docs/decision_engine.md's Technical+ML+Portfolio scoring model. Both are optional
// (nil-safe) and neutral (0.5) when unavailable, so the blend degrades to technical-only
// exactly as before when ML/Portfolio aren't wired in (see NewOrchestrator vs
// NewOrchestratorWithML).
func (o *Orchestrator) blendMLAndPortfolio(symbol string, bars []market.Bar, technical domainDecision.TechnicalScore) (overallScore, mlScore, portfolioScore float64, reasons []string) {
	overallScore = technical.Overall
	mlScore = 0.5
	if o.ML != nil {
		feature := mlUC.BuildFeatureVector(o.Analyzer, symbol, bars)
		if prediction, err := o.ML.Predict(feature); err == nil {
			mlScore = prediction.Score
		}
	}

	portfolioScore = 0.5
	if o.Portfolio != nil {
		if positions, err := o.Portfolio.GetPositions(); err == nil {
			portfolioScore = portfolioRiskSignal(symbol, positions)
		}
	}

	// Gated on ML specifically (not "ML or Portfolio"): every existing NewOrchestrator call
	// site already passes a non-nil Portfolio, so gating on either would have silently
	// changed behavior everywhere instead of only where NewOrchestratorWithML opts in.
	// Portfolio's non-nil-ness on its own does not enable the blend.
	if o.ML != nil {
		// Technical stays dominant; ML and portfolio are secondary/tertiary signals. ML is
		// currently RSI-derived too (see ml.UseCase.Predict), so this is partly redundant
		// with the RSI blend in buildTechnicalScore until a real trained model exists —
		// wiring the composition now means Atlas picks up a genuine ML signal automatically
		// the moment Predict() stops being a stub, with no further orchestration changes.
		overallScore = clamp01(technical.Overall*0.55 + mlScore*0.30 + portfolioScore*0.15)
	}

	if mlScore >= 0.6 {
		reasons = append(reasons, "ML signal supportive")
	} else if mlScore <= 0.4 {
		reasons = append(reasons, "ML signal cautious")
	}
	if portfolioScore < 0.5 {
		reasons = append(reasons, "Portfolio risk context reduces conviction")
	}

	return overallScore, mlScore, portfolioScore, reasons
}

// portfolioRiskSignal is a placeholder heuristic: a large unrealized loss is a risk signal
// (bias away from adding to a losing position), a large unrealized gain is a profit-taking
// signal (bias away from chasing further, since Atlas has no REDUCE action yet — see
// docs/decision_engine.md). No position, or a middling P&L, is neutral.
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
