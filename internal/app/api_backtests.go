package app

import (
	backtestingUC "atlas/internal/application/backtesting"
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	backtestingDomain "atlas/internal/domain/backtesting"

	"go.uber.org/zap"
)

// backtestRequestBudget bounds a whole backtest request: the wait for an admission slot (30s, see
// backtesting.Admission) plus the simulation that follows it. The server's WriteTimeout is 10s —
// shorter than the queue wait alone — so a queued request could wait, run a full simulation, and
// only then discover the connection's write deadline had expired, with the simulation itself
// never cancelled because WriteTimeout does not touch the request context.
//
// One number, applied to both, so the queue, the simulation and the response agree on when the
// request is over. Generous rather than tight: the alternative to waiting is an asynchronous job
// resource, which is a lot of machinery for a page a person is sitting in front of.
const backtestRequestBudget = 90 * time.Second

// beginBacktest gives a backtest request its full budget and returns the context every stage of
// it runs under. Callers must defer the returned cancel.
func (a *Application) beginBacktest(w http.ResponseWriter, r *http.Request) (context.Context, context.CancelFunc) {
	a.extendWriteDeadline(w, backtestRequestBudget)
	return context.WithTimeout(r.Context(), backtestRequestBudget)
}

// JSON API for backtesting: single runs, walk-forward analysis, and the persisted run history.
// Split out of handlers.go by resource. Read docs/backtesting.md's "Execution model" before
// trusting any number these return as money.

// backtestsHandler runs a historical simulation of the real decision pipeline for a symbol.
//
// @Summary Run a backtest simulation for a symbol
// @Description Simulates the live decision engine's current thresholds over historical bars and
// @Description persists the result to backtest history.
// @Tags Backtests
// @Produce json
// @Param symbol query string true "Ticker, e.g. AAPL"
// @Param window query int false "Lookback in days (default: 180)"
// @Param capital query number false "Starting capital (default: 10000)"
// @Param strategy query string false "Strategy name label (default: atlas-orchestrator)"
// @Param version query string false "Strategy version label (default: v1)"
// @Success 200 {object} backtestingDomain.StrategyResult
// @Failure 400 {object} map[string]string
// @Failure 502 {object} map[string]string "provider unreachable"
// @Router /backtests [get]
func (a *Application) backtestsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeError(w, http.StatusBadRequest, errors.New("symbol query parameter is required"))
		return
	}
	ctx, cancel := a.beginBacktest(w, r)
	defer cancel()

	window := backtestDefaultWindow
	if raw := r.URL.Query().Get("window"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			window = parsed
		}
	}
	capital := backtestDefaultCapital
	if raw := r.URL.Query().Get("capital"); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil && parsed > 0 {
			capital = parsed
		}
	}
	strategyName := r.URL.Query().Get("strategy")
	if strategyName == "" {
		strategyName = "atlas-orchestrator"
	}
	strategyVersion := r.URL.Query().Get("version")
	if strategyVersion == "" {
		strategyVersion = "v1"
	}

	bars, err := a.collectBars(ctx, symbol, window)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	result, err := a.Backtesting.WithContext(ctx).Run(symbol, bars, capital, strategyName, strategyVersion)
	if errors.Is(err, backtestingUC.ErrBusy) {
		// A load signal, not a failure: 503 with Retry-After is what a client can act on, and it
		// keeps a saturated instance out of the 5xx error rate as a bug.
		w.Header().Set("Retry-After", "10")
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.persistBacktestRun(result)
	writeJSON(w, http.StatusOK, result)
}

// backtestWalkForwardHandler runs docs/backtesting.md's walk-forward train/validate/test split
// with threshold optimization (see backtesting.UseCase.RunWalkForward) — distinct from
// backtestsHandler, which runs one simulation against the live decision engine's current
// thresholds over the whole window with no splitting or optimization. Not persisted: this is a
// report for a human to review (BestThresholds), not a decision that gets replayed later — see
// docs/backtesting.md's Implementation status note.
// @Summary Run a walk-forward backtest with threshold optimization
// @Description Splits the window 60/20/20 (train/validate/test) and optimizes candidate
// @Description thresholds against real historical data — a report for a human to review, not
// @Description persisted or replayed like a plain backtest.
// @Tags Backtests
// @Produce json
// @Param symbol query string true "Ticker, e.g. AAPL"
// @Param window query int false "Lookback in days (default: 500)"
// @Param capital query number false "Starting capital (default: 10000)"
// @Param strategy query string false "Strategy name label (default: atlas-orchestrator)"
// @Param version query string false "Strategy version label (default: v1)"
// @Success 200 {object} backtestingDomain.WalkForwardResult
// @Failure 400 {object} map[string]string
// @Failure 502 {object} map[string]string "provider unreachable"
// @Router /backtests/walkforward [get]
func (a *Application) backtestWalkForwardHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeError(w, http.StatusBadRequest, errors.New("symbol query parameter is required"))
		return
	}
	ctx, cancel := a.beginBacktest(w, r)
	defer cancel()

	window := backtestWalkForwardDefaultWindow
	if raw := r.URL.Query().Get("window"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			window = parsed
		}
	}
	capital := backtestDefaultCapital
	if raw := r.URL.Query().Get("capital"); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil && parsed > 0 {
			capital = parsed
		}
	}
	strategyName := r.URL.Query().Get("strategy")
	if strategyName == "" {
		strategyName = "atlas-orchestrator"
	}
	strategyVersion := r.URL.Query().Get("version")
	if strategyVersion == "" {
		strategyVersion = "v1"
	}

	bars, err := a.collectBars(ctx, symbol, window)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	result, err := a.Backtesting.WithContext(ctx).RunWalkForward(symbol, bars, capital, strategyName, strategyVersion)
	if errors.Is(err, backtestingUC.ErrBusy) {
		w.Header().Set("Retry-After", "30") // walk-forward runs long; do not invite an immediate retry
		writeError(w, http.StatusServiceUnavailable, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// backtestHistoryHandler lists previously persisted backtest runs for a symbol (migrations/005's
// backtest_runs table), newest first — distinct from backtestsHandler, which always executes a
// fresh simulation.
// @Summary List past backtest runs for a symbol
// @Description Reads previously persisted backtest runs (migrations/005), newest first — does
// @Description not execute a new simulation.
// @Tags Backtests
// @Produce json
// @Param symbol query string true "Ticker, e.g. AAPL"
// @Param limit query int false "Max rows to return (default: 20)"
// @Success 200 {object} map[string]any "{"runs": [...]}"
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /backtests/history [get]
func (a *Application) backtestHistoryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeError(w, http.StatusBadRequest, errors.New("symbol query parameter is required"))
		return
	}
	limit := backtestHistoryDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	runs, err := a.Storage.ListBacktestRuns(symbol, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// persistBacktestRun best-effort writes a backtest result to the history log (see
// migrations/005's backtest_runs table). Same non-fatal stance as persistRecommendation: a
// simulation the caller already received a truthful answer for shouldn't fail just because the
// audit log couldn't be written.
func (a *Application) persistBacktestRun(result backtestingDomain.StrategyResult) {
	if a.Storage == nil {
		return
	}
	if err := a.Storage.SaveBacktestRun(result); err != nil {
		a.Logger.Error("failed to persist backtest run", zap.String("symbol", result.Symbol), zap.Error(err))
	}
}
