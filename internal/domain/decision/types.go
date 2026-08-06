package decision

type TechnicalScore struct {
	RSI     float64 `json:"rsi"`
	EMA     float64 `json:"ema"`
	MACD    float64 `json:"macd"`
	ATR     float64 `json:"atr"`
	Volume  float64 `json:"volume"`
	Overall float64 `json:"overall"`
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
