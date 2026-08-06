package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	"atlas/internal/domain/backtesting"
)

// SaveBacktestRun appends a backtest result to the history log (migrations/005's backtest_runs
// table). Like recommendations/predictions, backtest runs are append-only — every call to
// backtesting.UseCase.Run is its own row, never upserted.
func (t *TimescaleDB) SaveBacktestRun(result backtesting.StrategyResult) error {
	ctx := context.Background()

	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded below

	symbolID, err := upsertSymbol(ctx, tx, result.Symbol)
	if err != nil {
		return err
	}

	summary, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal backtest result summary for %s: %w", result.Symbol, err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO backtest_runs (symbol_id, strategy_name, strategy_version, started_at, finished_at, result_summary)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		symbolID, result.StrategyName, result.StrategyVersion, result.StartedAt, result.FinishedAt, summary); err != nil {
		return fmt.Errorf("insert backtest run for %s: %w", result.Symbol, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// ListBacktestRuns returns the most recent persisted backtest runs for a symbol, newest first.
func (t *TimescaleDB) ListBacktestRuns(symbol string, limit int) ([]backtesting.BacktestRun, error) {
	ctx := context.Background()

	rows, err := t.db.QueryxContext(ctx,
		`SELECT r.id, s.ticker, r.strategy_name, r.strategy_version, r.started_at, r.finished_at, r.result_summary, r.created_at
		 FROM backtest_runs r
		 JOIN symbols s ON s.id = r.symbol_id
		 WHERE s.ticker = $1
		 ORDER BY r.created_at DESC
		 LIMIT $2`, symbol, limit)
	if err != nil {
		return nil, fmt.Errorf("list backtest runs for %s: %w", symbol, err)
	}
	defer func() { _ = rows.Close() }()

	runs := make([]backtesting.BacktestRun, 0)
	for rows.Next() {
		var run backtesting.BacktestRun
		var summary []byte
		if err := rows.Scan(&run.ID, &run.Symbol, &run.StrategyName, &run.StrategyVersion,
			&run.StartedAt, &run.FinishedAt, &summary, &run.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan backtest run for %s: %w", symbol, err)
		}
		if err := json.Unmarshal(summary, &run.ResultSummary); err != nil {
			return nil, fmt.Errorf("unmarshal backtest result summary for %s: %w", symbol, err)
		}
		runs = append(runs, run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate backtest runs for %s: %w", symbol, err)
	}
	return runs, nil
}
