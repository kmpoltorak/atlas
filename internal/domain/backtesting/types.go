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
	Symbol          string        `json:"symbol"`
	InitialCapital  float64       `json:"initial_capital"`
	StrategyName    string        `json:"strategy_name"`
	StrategyVersion string        `json:"strategy_version"`
	StartedAt       time.Time     `json:"started_at"`
	FinishedAt      time.Time     `json:"finished_at"`
	TotalReturn     float64       `json:"total_return"`
	AnnualReturn    float64       `json:"annual_return"`
	SharpeRatio     float64       `json:"sharpe_ratio"`
	Volatility      float64       `json:"volatility"`
	MaxDrawdown     float64       `json:"max_drawdown"`
	TradeCount      int           `json:"trade_count"`
	WinningTrades   int           `json:"winning_trades"`
	LosingTrades    int           `json:"losing_trades"`
	WinRate         float64       `json:"win_rate"`
	Trades          []Trade       `json:"trades"`
	MLEvaluation    *MLEvaluation `json:"ml_evaluation,omitempty"`
}

// MLEvaluation reports how well the ML model's correction-probability predictions matched
// what actually happened over the backtest window, per docs/backtesting.md's "ML Evaluation"
// section. Nil when no ML model was loaded (see decision.Orchestrator.ML), since there's no
// real prediction stream to evaluate — never fabricated as a zero-value struct in that case.
type MLEvaluation struct {
	SampleSize int     `json:"sample_size"`
	Accuracy   float64 `json:"accuracy"`
	Precision  float64 `json:"precision"`
	Recall     float64 `json:"recall"`
	F1Score    float64 `json:"f1_score"`
	ROCAUC     float64 `json:"roc_auc"`
}

// BacktestRun stores a single historical validation record, one row per call to
// backtesting.UseCase.Run — see internal/infrastructure/persistence's SaveBacktestRun /
// ListBacktestRuns.
type BacktestRun struct {
	ID              string         `json:"id"`
	Symbol          string         `json:"symbol"`
	StrategyName    string         `json:"strategy_name"`
	StrategyVersion string         `json:"strategy_version"`
	StartedAt       time.Time      `json:"started_at"`
	FinishedAt      time.Time      `json:"finished_at"`
	CreatedAt       time.Time      `json:"created_at"`
	ResultSummary   StrategyResult `json:"result_summary"`
}
