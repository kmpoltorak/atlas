package portfolio

type Position struct {
	Symbol       string  `json:"symbol"`
	Quantity     float64 `json:"quantity"`
	AverageCost  float64 `json:"average_cost"`
	CurrentPrice float64 `json:"current_price"`
}

type Transaction struct {
	Symbol    string  `json:"symbol"`
	Type      string  `json:"type"`
	Quantity  float64 `json:"quantity"`
	Price     float64 `json:"price"`
	Timestamp string  `json:"timestamp"`
}

type RiskSnapshot struct {
	Exposure   float64 `json:"exposure"`
	Drawdown   float64 `json:"drawdown"`
	Volatility float64 `json:"volatility"`
}
