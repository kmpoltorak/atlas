package decision

import (
	"math"
	"sync/atomic"

	analyzerUC "atlas/internal/application/analyzer"
	mlUC "atlas/internal/application/ml"
	portfolioUC "atlas/internal/application/portfolio"
	domainDecision "atlas/internal/domain/decision"
	"atlas/internal/domain/market"
	domainML "atlas/internal/domain/ml"
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

	// mlEnabled is a runtime on/off switch for the ML+Portfolio+Risk blend, independent of
	// whether ML is wired in at all (see NewOrchestrator vs NewOrchestratorWithML) — the
	// Account dashboard page flips this live (see SetMLEnabled) so a toggle takes effect
	// immediately, with no restart needed, unlike the ML_ENABLED env var it defaults from
	// (config.Config.MLEnabled). Defaults true: unset, it preserves NewOrchestratorWithML's
	// existing behavior exactly.
	mlEnabled atomic.Bool
}

func NewOrchestrator(analyzerUC *analyzerUC.UseCase, portfolioUC *portfolioUC.UseCase, decisionUC *UseCase) *Orchestrator {
	o := &Orchestrator{
		Analyzer:  analyzerUC,
		Portfolio: portfolioUC,
		Decision:  decisionUC,
	}
	o.mlEnabled.Store(true)
	return o
}

// NewOrchestratorWithML wires the ML use case in as well, enabling the full Technical+ML+
// Portfolio blend in buildTechnicalScore. Kept separate from NewOrchestrator so existing
// technical-only call sites and tests don't need an ML use case they don't care about.
func NewOrchestratorWithML(analyzerUC *analyzerUC.UseCase, portfolioUC *portfolioUC.UseCase, decisionUC *UseCase, mlUC *mlUC.UseCase) *Orchestrator {
	o := NewOrchestrator(analyzerUC, portfolioUC, decisionUC)
	o.ML = mlUC
	return o
}

// MLEnabled reports whether the ML+Portfolio+Risk blend is currently switched on (see
// mlEnabled) — read by the Account page to render the toggle's current state.
func (o *Orchestrator) MLEnabled() bool {
	return o.mlEnabled.Load()
}

// SetMLEnabled flips the runtime ML-blend toggle — called by the Account page's handler after
// durably recording the choice (persistence.TimescaleDB.SetMLEnabled), and by the composition
// root at startup to apply config.Config.MLEnabled/the durable override. Safe to call whether or
// not ML was wired in at construction (NewOrchestrator vs NewOrchestratorWithML); with o.ML nil,
// blendMLAndPortfolio's gate on o.ML already skips the blend regardless of this flag.
func (o *Orchestrator) SetMLEnabled(enabled bool) {
	o.mlEnabled.Store(enabled)
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
	recommendation.RiskDataAvailable = blend.RiskDataAvailable
	recommendation.MarketCondition = mlUC.ClassifyMarketCondition(o.Analyzer, symbol, bars)
	recommendation.ModelVersion = EngineVersion
	recommendation.MLModelVersion = blend.MLModelVersion
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
	momentumVolume := momentumVolumeScore(o.Analyzer, symbol, bars)

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
	// RiskDataAvailable is false when RiskScore is 1.0 only because there was no exposure to
	// measure (empty portfolio), not because the portfolio was checked and found low-risk — see
	// computeRiskScore.
	RiskDataAvailable bool
	MLConfidence      float64
	// MLModelVersion is ml.Prediction.ModelVersion — carried through to Recommendation.
	// MLModelVersion so a caller can see which real model (or the stub) actually produced
	// MLScore, distinct from Recommendation.ModelVersion (the decision engine's own tag).
	MLModelVersion string
	HasPosition    bool
	Reasons        []string
}

// mlScoreFromPrediction turns the ML models' raw outputs into the bullish-lean term the blend
// consumes, folding in recovery probability — which was previously computed, persisted, shown on
// the dashboard, and then ignored by every decision.
//
// The risk being scored is not "will it drop" but "will it drop *and stay down*". A dip that
// recovers inside the recovery model's horizon is not a reason to avoid a position; treating it
// as one throws away the distinction the recovery model exists to draw. So the penalty applied
// here is the joint probability of an unrecovered drop:
//
//	P(unrecovered drop) = P(correction) * (1 - P(recovery))
//
// which is subtracted from 1 to keep the term oriented the way the blend and its >=0.6/<=0.4
// reason thresholds already expect. When no recovery model is loaded RecoveryProbability is 0
// (see ml.UseCase.Predict), so this degrades exactly to the previous 1 - P(correction) — the
// recovery model is optional here in the same way it is everywhere else.
//
// This deliberately does not attempt to fix the deeper asymmetry that a low correction
// probability still reads as mildly bullish, so a becalmed instrument scores well on this term
// without any upside signal behind it. Restructuring the blend into separate risk and
// opportunity terms is a larger change to how every recommendation is produced, and is recorded
// as such in docs/decision_engine.md rather than smuggled in here.
func mlScoreFromPrediction(prediction domainML.Prediction) float64 {
	if prediction.ModelVersion == domainML.StubModelVersion {
		// The stub has no correction-probability concept at all — its Score is an RSI-derived
		// placeholder, and reinterpreting it through a risk formula would be inventing meaning.
		return prediction.Score
	}
	unrecoveredDrop := prediction.CorrectionProbability * (1 - prediction.RecoveryProbability)
	return clamp01(1 - unrecoveredDrop)
}

// riskFactor converts one of the three risk signals into a multiplier on the technical score.
// 1 means "nothing to penalize"; smaller means the signal found a reason for caution.
//
// This replaced a four-term weighted average (0.50 technical / 0.25 ML / 0.15 portfolio / 0.10
// risk). The average was the wrong shape, because only one of its four terms measures an
// opportunity. The other three measure risk, and a risk model that finds no risk was being read
// as a reason to buy:
//
//   - ml.Prediction estimates P(correction) and P(recovery). There is no upside model at all, so
//     a calm instrument scored 0.97 on this term with no bullish evidence behind it whatsoever.
//     Across the live watchlist on 2026-08-10 the term spanned 0.810-0.997 — a near-constant
//     contributing a flat lift to every symbol.
//   - portfolioRiskSignal already tops out at 0.5, so as an averaged term it dragged *every*
//     score toward the middle even when it had nothing to say.
//   - computeRiskScore returns 1.0 for an empty portfolio (no drawdown, no volatility to
//     measure), which the average read as maximum enthusiasm. Absence of a portfolio is not a
//     buy signal; it is an absence of information.
//
// Together those three contributed roughly a constant +0.15 to every recommendation, which is
// why the BUY threshold had drifted into meaning something other than what it says. As factors,
// each one now does exactly what it measures: it can lower a technical signal, never invent one.
// The multiplication also compounds, which is the intended reading — a weak position inside a
// drawdown inside a correction warning is worse than any of the three alone.
func riskFactor(score float64) float64 {
	return clamp01(score)
}

// blendMLAndPortfolio folds the ML prediction, per-symbol portfolio P&L, and portfolio-wide
// risk context into the technical score, per docs/decision_engine.md's Technical+ML+Portfolio+
// Risk scoring model. All three are optional (nil-safe) and neutral when unavailable, so the
// blend degrades to technical-only exactly as before when ML/Portfolio aren't wired in (see
// NewOrchestrator vs NewOrchestratorWithML). MLConfidence defaults to 1.0 (never triggers
// decision.UseCase's WAIT band) when ML isn't wired in — there's no real confidence signal to
// distrust in that mode.
func (o *Orchestrator) blendMLAndPortfolio(symbol string, bars []market.Bar, technical domainDecision.TechnicalScore) blendResult {
	// Defaults are "no penalty", not "neutral": these are risk factors now, and a signal that
	// never ran must leave the technical score untouched rather than halving it. PortfolioScore's
	// no-penalty value is 0.5, the top of portfolioRiskSignal's own scale.
	result := blendResult{Overall: technical.Overall, MLScore: 1.0, PortfolioScore: 0.5, RiskScore: 1.0, MLConfidence: 1.0}

	// mlActive also gates the runtime mlEnabled toggle (see SetMLEnabled), so the Account page's
	// switch reproduces exactly what NewOrchestrator (ML never wired in) already does for the
	// prediction call and the multiplication below: no prediction call, no ML/Portfolio/Risk
	// multiplication, MLConfidence stays at its never-triggers-WAIT default, MLModelVersion stays
	// empty. Portfolio position/risk data is deliberately NOT gated on this — HasPosition (BUY vs
	// ACCUMULATE, SELL vs REDUCE) reflects real portfolio state independent of whether the ML
	// blend is switched on, exactly as it already does when ML was never wired in at all.
	mlActive := o.ML != nil && o.mlEnabled.Load()

	if mlActive {
		feature := mlUC.BuildFeatureVector(o.Analyzer, symbol, bars)
		if prediction, err := o.ML.Predict(feature); err == nil {
			result.MLScore = mlScoreFromPrediction(prediction)
			result.MLConfidence = prediction.Confidence
			result.MLModelVersion = prediction.ModelVersion
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
			result.RiskDataAvailable = risk.Exposure > 0
		}
	}

	// Gated on ML specifically (not "ML or Portfolio"): every existing NewOrchestrator call
	// site already passes a non-nil Portfolio, so gating on either would have silently changed
	// behavior everywhere instead of only where NewOrchestratorWithML opts in — Portfolio's
	// non-nil-ness on its own does not enable the blend.
	if mlActive {
		// Technical stays dominant; ML/Portfolio/Risk are secondary signals. ML is currently
		// RSI-derived too (see ml.UseCase.Predict) when no trained model is loaded, so this is
		// partly redundant with the RSI blend in buildTechnicalScore until a real model exists —
		// wiring the composition now means Atlas picks up a genuine ML signal automatically the
		// moment Predict() stops being a stub, with no further orchestration changes.
		// portfolioRiskSignal lives on a [0.3, 0.5] scale where 0.5 is "nothing to say", so it is
		// rescaled to a factor here rather than being used raw. The other two already run 0..1
		// with 1 meaning no penalty.
		result.Overall = clamp01(technical.Overall *
			riskFactor(result.MLScore) *
			riskFactor(result.PortfolioScore/0.5) *
			riskFactor(result.RiskScore))
	}

	// Only the cautious direction is reported. "ML signal supportive" used to fire whenever the
	// correction model was quiet, which is almost always — it told the reader that a model with
	// no upside component was endorsing a buy.
	if result.MLScore <= 0.4 {
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

// Momentum-term parameters, chosen by measuring the candidates against the 41807 real closed
// hourly bars behind the live watchlist (47 symbols, 274-1014 bars each) rather than by taste:
//
//   - momentumWindow 12 bars, roughly two trading sessions. The normalized distribution is flat
//     across 3/6/12/24 (median ~0, p95 ~1.8 volatility units), so this is not a knife-edge
//     choice; 12 sits where the interquartile spread is widest without reaching back so far that
//     the shorter-history symbols lose readings.
//   - momentumVolWindow 20, reusing analyzer.ComputeRealizedVolatility's short window so the
//     denominator here means the same thing it means in the ML feature vector.
//   - momentumScale 1.5 volatility units for a saturated signal. Measured over the same bars,
//     1.0 saturates (p05 0.040, p95 0.971 — the tails pile up at the ends) and 2.0 compresses
//     (p95 0.853, so a genuinely strong move never reads as strong). 1.5 spends the whole range:
//     p05 0.139, p25 0.330, p50 0.509, p75 0.695, p95 0.880.
const (
	momentumWindow    = 12
	momentumVolWindow = 20
	momentumScale     = 1.5
)

// momentumVolumeScore is the heaviest term in buildTechnicalScore (weight 0.45), and it reports
// whether price has moved meaningfully over the recent window, confirmed by volume.
//
// It replaces a two-valued step function that read a single bar: momentum > 2% on that one bar
// AND raw volume/1000 > 1 returned 0.82, everything else returned 0.48. That had three defects,
// each measured against production rather than argued:
//
//   - It read only the newest bar, which Yahoo serves as an unclosed live quote with open ==
//     close and volume 0 (see providers.dropUnclosedBars). Momentum was therefore identically 0
//     and the bullish branch could never fire on live traffic — the largest term in the score
//     was frozen at 0.48 permanently. Every BUY recorded in the week to 2026-08-10 landed in
//     0.750-0.754 against a 0.75 threshold, i.e. grazing the line, and flickered back to HOLD on
//     the next poll. No SELL or REDUCE was reachable at all.
//   - Even on correct closed bars the rule fires on 3.21% of them, so it was near-constant with
//     occasional 0.34-wide jumps — a quantization artifact driving the recommendation, not a
//     signal. The replacement moves 0.053 per bar at the median (0.012 in the blended score).
//   - volume/1000 hard-codes the share-volume scale of a liquid US equity. The gate passes on
//     92% of bars, so it barely gated anything, while meaning something entirely different for a
//     thinly traded WSE listing. ComputeVolume already produces the scale-free measure (volume
//     over the symbol's own average) and was being ignored.
//
// The replacement normalizes the window return by the instrument's own realized volatility, so
// one threshold means the same thing for ORCL and for ETFBDIVPL.WA — the same scale-free stance
// the ML feature vector settled on. tanh keeps it smooth and bounded without a clamp. Thin
// volume damps the reading toward neutral rather than reversing it, which is the ordinary
// reading of an unconfirmed move: a move nobody traded is weak evidence, not counter-evidence.
//
// Returns a neutral 0.5 whenever the inputs cannot support a reading (no analyzer, too little
// history, a non-positive past close, or an instrument with no measured volatility) — "no
// information" rather than a fabricated lean.
func momentumVolumeScore(analyzer *analyzerUC.UseCase, symbol string, bars []market.Bar) float64 {
	if analyzer == nil || len(bars) <= momentumWindow {
		return 0.5
	}
	latest := len(bars) - 1

	past := bars[latest-momentumWindow].Close
	if past <= 0 {
		return 0.5
	}
	momentum := bars[latest].Close/past - 1

	volatility, err := analyzer.ComputeRealizedVolatility(symbol, bars, momentumVolWindow)
	if err != nil || len(volatility) <= latest || volatility[latest] <= 0 {
		return 0.5
	}
	// Volatility is per-bar, so scaling it to the window's length is what makes the ratio a
	// count of standard deviations for a move of this duration.
	normalized := momentum / (volatility[latest] * math.Sqrt(momentumWindow))
	score := 0.5 + 0.5*math.Tanh(normalized/momentumScale)

	// Confirmation spans [0.5, 1.0]: at or above the symbol's average volume the reading stands
	// as measured, and below it the deviation from neutral is scaled down, reaching half weight
	// only at zero volume. Capped at 1 so an extraordinary volume spike cannot amplify the term
	// past its own bounds.
	confirmation := 0.5
	if series, err := analyzer.ComputeVolume(symbol, bars); err == nil && len(series) > latest {
		confirmation = 0.5 + 0.5*math.Min(1, series[latest])
	}

	return clamp01(0.5 + (score-0.5)*confirmation)
}

func clamp01(value float64) float64 {
	return math.Min(1, math.Max(0, value))
}
