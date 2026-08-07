package decision

type TechnicalScore struct {
	RSI     float64 `json:"rsi"`
	EMA     float64 `json:"ema"`
	MACD    float64 `json:"macd"`
	ATR     float64 `json:"atr"`
	Volume  float64 `json:"volume"`
	Overall float64 `json:"overall"`
}

// RecommendationInput bundles everything BuildRecommendation needs to differentiate
// ACCUMULATE/REDUCE/WAIT from BUY/SELL/HOLD: the blended technical score alone can't tell
// "opening a new position" from "adding to one already held", or "a real but untrustworthy
// signal" from "no signal at all".
type RecommendationInput struct {
	Technical TechnicalScore

	// HasPosition reflects real portfolio state (Portfolio.GetPositions), not score/P&L —
	// per docs/decision_engine.md, BUY/SELL are gated purely on whether a position already
	// exists, independent of how profitable it currently is.
	HasPosition bool

	// MLConfidence is the ML model's confidence in the current reading (ml.Prediction.
	// Confidence). Defaults to 1.0 (never triggers WAIT) when ML isn't wired in at all
	// (plain NewOrchestrator, no model), since there's no real confidence signal to distrust
	// in that mode — see Orchestrator.blendMLAndPortfolio.
	MLConfidence float64
}

// Thresholds configures BuildRecommendation's score bands (see docs/decision_engine.md's
// "Recommendation Types" and application/decision.UseCase.BuildRecommendation). DefaultThresholds
// preserves the original hand-picked cutoffs used everywhere in live traffic;
// backtesting.UseCase.Optimize searches variations of these against historical data (see
// docs/backtesting.md's "Optimization" section) without ever changing what's live — an
// optimization result is reported back for a human to review, never applied automatically.
type Thresholds struct {
	StrongBullish   float64 `json:"strong_bullish"`
	LeanBullish     float64 `json:"lean_bullish"`
	LeanBearish     float64 `json:"lean_bearish"`
	StrongBearish   float64 `json:"strong_bearish"`
	LowMLConfidence float64 `json:"low_ml_confidence"`
}

// DefaultThresholds returns the original, hand-picked cutoffs — first-pass documented
// heuristics, same honesty stance as the 0.75/0.4 BUY/SELL cutoffs they originally replaced.
func DefaultThresholds() Thresholds {
	return Thresholds{
		StrongBullish:   0.75,
		LeanBullish:     0.55, // [LeanBullish, StrongBullish): leaning bullish, not yet strong
		LeanBearish:     0.45, // (StrongBearish, LeanBearish]: leaning bearish, not yet strong
		StrongBearish:   0.25,
		LowMLConfidence: 0.30, // gates WAIT vs HOLD — see BuildRecommendation's waitOrHold
	}
}

// Valid reports whether the thresholds preserve BuildRecommendation's required band ordering
// (StrongBearish <= LeanBearish <= LeanBullish <= StrongBullish, all within [0,1]) and a
// LowMLConfidence within [0,1]. An out-of-order set would make BuildRecommendation's band
// checks produce overlapping or unreachable bands — callers that vary thresholds (e.g.
// backtesting's optimizer) must skip any candidate this rejects.
func (t Thresholds) Valid() bool {
	return t.StrongBearish >= 0 && t.StrongBearish <= t.LeanBearish &&
		t.LeanBearish <= t.LeanBullish && t.LeanBullish <= t.StrongBullish &&
		t.StrongBullish <= 1 && t.LowMLConfidence >= 0 && t.LowMLConfidence <= 1
}

type Recommendation struct {
	Symbol     string   `json:"symbol"`
	Action     string   `json:"action"`
	Confidence float64  `json:"confidence"`
	Reason     []string `json:"reason"`

	// Score is the final blended score (Technical/ML/Portfolio/Risk, see Orchestrator.
	// blendMLAndPortfolio); the component fields below are the inputs that produced it. All
	// five are zero when a recommendation short-circuits before scoring (e.g. no bar data).
	Score          float64 `json:"score"`
	TechnicalScore float64 `json:"technical_score"`
	MLScore        float64 `json:"ml_score"`
	PortfolioScore float64 `json:"portfolio_score"`

	// RiskScore is a portfolio-wide risk signal (see portfolio.UseCase.ComputeRisk), distinct
	// from PortfolioScore (a single symbol's own P&L) — it reflects overall exposure/drawdown/
	// volatility across every held position, per docs/decision_engine.md's "Risk Context" being
	// a portfolio-wide concept. On the same [0,1] scale as the other components (higher = more
	// supportive of buying).
	RiskScore float64 `json:"risk_score"`

	// MarketCondition is BULL/BEAR/SIDEWAYS (see ml.ClassifyMarketCondition) — a deterministic
	// ADX+EMA technical rule, not a trained model. Empty string when there isn't enough history
	// to compute it (see ml.ClassifyMarketCondition's own warm-up requirement).
	MarketCondition string `json:"market_condition,omitempty"`

	ModelVersion string `json:"model_version"`
}
