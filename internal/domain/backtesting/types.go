package backtesting

import "time"

// StrategyResult captures the summary of a single backtest execution.
type StrategyResult struct {
	StrategyName    string    `json:"strategy_name"`
	StrategyVersion string    `json:"strategy_version"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	TotalReturn     float64   `json:"total_return"`
	SharpeRatio     float64   `json:"sharpe_ratio"`
	MaxDrawdown     float64   `json:"max_drawdown"`
	Trades          int       `json:"trades"`
	WinRate         float64   `json:"win_rate"`
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
