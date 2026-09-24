package backtesting

import (
	"time"

	"atlas/internal/domain/decision"
)

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

	// MLContribution answers the question MLEvaluation cannot: does the model make money?
	// Nil when no real trained model is loaded, same guard as MLEvaluation.
	MLContribution *MLContribution `json:"ml_contribution,omitempty"`
}

// MLContribution reports what the ML signal is actually worth to the strategy, by running the
// identical simulation twice over the same bars — once with the model wired into the decision
// blend, once with it removed (the blend degrades to technical-only, exactly as it does when no
// model is configured) — and reporting the difference.
//
// This exists because statistical scores can't answer the only question that matters for a
// trading signal. MLEvaluation reports ROC AUC, precision and recall; none of them say whether
// acting on the model beats not acting on it. A model can rank well and still lose money (if it
// is right about moves too small to trade, or right too late), and it can score modestly and
// still help (if it is right about the few moves that are large). Judging a trading model by
// classification metrics alone is the standard mistake this struct is here to prevent, and it
// was previously the position this project was in: the backtester already computed Sharpe, and
// the ML evaluation already computed AUC, and nothing ever compared the two.
//
// Both runs share thresholds, capital, bars and simulated portfolio logic, so the delta is
// attributable to the ML term and nothing else.
type MLContribution struct {
	// WithML mirrors the parent StrategyResult's own numbers (the run that was actually
	// reported), repeated here so the comparison reads as a pair without cross-referencing.
	WithMLTotalReturn    float64 `json:"with_ml_total_return"`
	WithMLSharpeRatio    float64 `json:"with_ml_sharpe_ratio"`
	WithMLMaxDrawdown    float64 `json:"with_ml_max_drawdown"`
	WithMLTradeCount     int     `json:"with_ml_trade_count"`
	WithMLWinRate        float64 `json:"with_ml_win_rate"`
	WithoutMLTotalReturn float64 `json:"without_ml_total_return"`
	WithoutMLSharpeRatio float64 `json:"without_ml_sharpe_ratio"`
	WithoutMLMaxDrawdown float64 `json:"without_ml_max_drawdown"`
	WithoutMLTradeCount  int     `json:"without_ml_trade_count"`
	WithoutMLWinRate     float64 `json:"without_ml_win_rate"`

	// ReturnDelta and SharpeDelta are with-minus-without. Positive means the model earned its
	// place in the blend over this window; negative means the strategy would have done better
	// ignoring it, which is a real and reportable outcome, not a bug to be hidden.
	ReturnDelta float64 `json:"return_delta"`
	SharpeDelta float64 `json:"sharpe_delta"`

	// DrawdownDelta is with-minus-without, so **negative is good** here (the model reduced the
	// worst peak-to-trough loss). Signed consistently with the others rather than flipped for
	// convenience, since a silently inverted sign is exactly the kind of thing that gets
	// misread months later.
	DrawdownDelta float64 `json:"drawdown_delta"`
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
	// Label describes the correction event the predictions were scored against, and
	// LabelFromModel whether it is the one the model was trained on (its metadata's
	// label_params) or the default, used only because the model does not record its own. A
	// false LabelFromModel means these numbers may compare the model against an event it never
	// learned.
	Label          string `json:"label"`
	LabelFromModel bool   `json:"label_from_model"`
}

// WalkForwardResult reports docs/backtesting.md's "Walk Forward Testing": the bar range is
// split chronologically into Train/Validate/Test (see application/backtesting.splitWalkForward
// — proportional, never shuffled). Train is the split searched for BestThresholds (see
// application/backtesting.optimizeThresholds); Validate re-runs those same thresholds on the
// immediate next out-of-sample split as an overfitting sanity check — its numbers should look
// broadly consistent with Train's, not just with Test's; Test is the final held-out split, never
// used for threshold selection at all, so Test's numbers are the honest estimate of real
// forward performance. BestThresholds is reported for a human to review and decide whether to
// manually update decision.DefaultThresholds — nothing here writes to the live decision engine
// automatically (see docs/backtesting.md's Implementation status note).
type WalkForwardResult struct {
	Symbol              string              `json:"symbol"`
	BestThresholds      decision.Thresholds `json:"best_thresholds"`
	CandidatesEvaluated int                 `json:"candidates_evaluated"`
	Train               StrategyResult      `json:"train"`
	Validate            StrategyResult      `json:"validate"`
	Test                StrategyResult      `json:"test"`
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
