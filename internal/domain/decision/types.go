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

type Recommendation struct {
	Symbol     string   `json:"symbol"`
	Action     string   `json:"action"`
	Confidence float64  `json:"confidence"`
	Reason     []string `json:"reason"`

	// Score is the final blended score (Technical/ML/Portfolio, see Orchestrator.
	// blendMLAndPortfolio); the component fields below are the inputs that produced it. All
	// four are zero when a recommendation short-circuits before scoring (e.g. no bar data).
	Score          float64 `json:"score"`
	TechnicalScore float64 `json:"technical_score"`
	MLScore        float64 `json:"ml_score"`
	PortfolioScore float64 `json:"portfolio_score"`
	ModelVersion   string  `json:"model_version"`
}
