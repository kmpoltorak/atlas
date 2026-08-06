-- Backtest runs (see docs/backtesting.md's Implementation status note and
-- internal/application/backtesting) are persisted as a single append-only history table, one
-- row per call to backtesting.UseCase.Run. docs/backtesting.md's target design lists five
-- separate tables (backtests, backtest_runs, trades, performance_metrics, strategy_versions);
-- this deliberately collapses them into one — trades and performance metrics are stored inline
-- in result_summary (JSONB), matching the recommendations table's explanation-as-JSONB
-- pattern, rather than normalizing them into their own tables with no query pattern that needs
-- it yet. strategy_versions isn't a real versioning concept here either, just a caller-supplied
-- tag string on each run.
CREATE TABLE IF NOT EXISTS backtest_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    symbol_id UUID NOT NULL REFERENCES symbols(id),
    strategy_name TEXT NOT NULL,
    strategy_version TEXT NOT NULL,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    result_summary JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_backtest_runs_symbol_created
    ON backtest_runs (symbol_id, created_at DESC);
