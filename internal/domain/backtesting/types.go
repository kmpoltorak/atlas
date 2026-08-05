package backtesting

import "time"

// Trade records a single simulated BUY/SELL action taken during a backtest, per
// docs/backtesting.md's requirement that every action include timestamp, price, quantity,
// reason and confidence.
type Trade struct {
	Timestamp  time.Time `json:"timestamp"`
	Symbol     string    `json:"symbol"`
	Action     string    `json:"action"`
	Price      float64   `json:"price"`
	Quantity   float64   `json:"quantity"`
	Reason     string    `json:"reason"`
	Confidence float64   `json:"confidence"`
}

// StrategyResult captures the summary of a single backtest execution.
type StrategyResult struct {
	StrategyName    string    `json:"strategy_name"`
	StrategyVersion string    `json:"strategy_version"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	TotalReturn     float64   `json:"total_return"`
	AnnualReturn    float64   `json:"annual_return"`
	SharpeRatio     float64   `json:"sharpe_ratio"`
	Volatility      float64   `json:"volatility"`
	MaxDrawdown     float64   `json:"max_drawdown"`
	TradeCount      int       `json:"trade_count"`
	WinningTrades   int       `json:"winning_trades"`
	LosingTrades    int       `json:"losing_trades"`
	WinRate         float64   `json:"win_rate"`
	Trades          []Trade   `json:"trades"`
}

// BacktestRun stores a single historical validation record.
type BacktestRun struct {
	ID              string         `json:"id"`
	StrategyName    string         `json:"strategy_name"`
	StrategyVersion string         `json:"strategy_version"`
	StartedAt       time.Time      `json:"started_at"`
	FinishedAt      time.Time      `json:"finished_at"`
	ResultSummary   StrategyResult `json:"result_summary"`
}
