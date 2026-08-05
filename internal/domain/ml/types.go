package ml

type FeatureVector struct {
	RSI    float64 `json:"rsi"`
	EMA    float64 `json:"ema"`
	MACD   float64 `json:"macd"`
	ATR    float64 `json:"atr"`
	Volume float64 `json:"volume"`
}

type Prediction struct {
	Symbol       string  `json:"symbol"`
	Score        float64 `json:"score"`
	Confidence   float64 `json:"confidence"`
	ModelVersion string  `json:"model_version"`
}
