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
}
