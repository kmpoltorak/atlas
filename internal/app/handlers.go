package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	mlUC "atlas/internal/application/ml"
	watchlistUC "atlas/internal/application/watchlist"
	"atlas/internal/domain/alerts"
	backtestingDomain "atlas/internal/domain/backtesting"
	"atlas/internal/domain/decision"
	"atlas/internal/domain/market"
	mlDomain "atlas/internal/domain/ml"
	"atlas/internal/domain/portfolio"
	"atlas/internal/domain/watchlist"
	"atlas/internal/infrastructure/persistence"

	"go.uber.org/zap"
)

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, errors.New("method not allowed"))
}

// windowFromQuery reads an optional ?window= override, falling back to the configured default.
func (a *Application) windowFromQuery(r *http.Request) int {
	if raw := r.URL.Query().Get("window"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			return parsed
		}
	}
	return a.Config.CollectorWindow
}

// collectBars wraps Collector.Collect with the provider metrics and error logging every
// HTTP entry point into the market data path needs, so those concerns live in one place.
// Cache hits are deliberately excluded from atlas_provider_requests_total — that metric
// exists to show real load/rate-limit exposure on the live provider, not request volume.
func (a *Application) collectBars(symbol string, window int) ([]market.Bar, error) {
	bars, fromCache, err := a.Collector.Collect(symbol, window)
	if fromCache {
		return bars, nil
	}
	outcome := "success"
	if err != nil {
		outcome = "error"
		a.Logger.Error("provider request failed",
			zap.String("provider", marketProviderLabel),
			zap.String("symbol", symbol),
			zap.Error(err),
		)
	}
	a.Metrics.ProviderRequests.WithLabelValues(marketProviderLabel, outcome, "bars").Inc()
	return bars, err
}

// refreshRecommendation recomputes and persists symbol's recommendation right after a
// transaction changes whether the portfolio holds it — opening a first position or selling one
// down to zero flips BuildRecommendation's HasPosition gate (BUY vs ACCUMULATE, SELL vs REDUCE).
// Without this, the previously-computed recommendation stays cached and visibly wrong (e.g.
// "REDUCE" showing for a symbol with no position — a real bug report) until the next alert-poll
// cycle recomputes it, up to ALERT_POLL_INTERVAL_SECONDS later. Runs in its own goroutine (called
// as "go a.refreshRecommendation(symbol)") since collectBars can be a real network call to the
// market provider — recording a transaction shouldn't wait on that. Best-effort: a failure here
// just logs, matching persistRecommendation's own stance — the transaction itself already
// succeeded and must not be affected by a stale-cache repair failing.
func (a *Application) refreshRecommendation(symbol string) {
	if a.Orchestrator == nil || a.Collector == nil {
		return
	}
	bars, err := a.collectBars(symbol, a.Config.CollectorWindow)
	if err != nil {
		a.Logger.Warn("failed to refresh recommendation after transaction", zap.String("symbol", symbol), zap.Error(err))
		return
	}
	rec, err := a.Orchestrator.BuildRecommendationFromBars(symbol, bars)
	if err != nil {
		a.Logger.Warn("failed to build recommendation after transaction", zap.String("symbol", symbol), zap.Error(err))
		return
	}
	a.Metrics.RecommendationsTotal.WithLabelValues(rec.Action).Inc()
	a.persistRecommendation(rec)
	a.persistIndicatorSnapshot(symbol, bars)
}

// transactionSymbol looks up id's symbol before RemoveTransaction deletes it — needed so the
// caller can still call refreshRecommendation with the right symbol after removal. Returns "" if
// not found (already gone, or the id never existed), the same no-op stance RemoveTransaction
// itself takes for an unknown id.
func (a *Application) transactionSymbol(id string) string {
	transactions, err := a.Portfolio.GetTransactions()
	if err != nil {
		return ""
	}
	for _, tx := range transactions {
		if tx.ID == id {
			return tx.Symbol
		}
	}
	return ""
}

// persistRecommendation best-effort writes a recommendation to the history log (see
// migrations/001's recommendations table). Unlike portfolio writes, a failure here doesn't
// fail the request: Atlas can still give a truthful live analysis even if the audit log isn't
// currently reachable, whereas silently dropping a trade record would corrupt real state.
func (a *Application) persistRecommendation(rec decision.Recommendation) {
	if a.Storage == nil {
		return
	}
	if err := a.Storage.SaveRecommendation(rec); err != nil {
		a.Logger.Error("failed to persist recommendation", zap.String("symbol", rec.Symbol), zap.Error(err))
	}
}

// persistPrediction best-effort writes a prediction to the history log (see migrations/001's
// predictions table / migrations/004's score column).
func (a *Application) persistPrediction(pred mlDomain.Prediction) {
	if a.Storage == nil {
		return
	}
	if err := a.Storage.SavePrediction(pred); err != nil {
		a.Logger.Error("failed to persist prediction", zap.String("symbol", pred.Symbol), zap.Error(err))
	}
}

// persistIndicatorSnapshot best-effort computes and writes the full indicator set (RSI, EMA,
// MACD, ATR, Volume, OBV, ADX, Bollinger Bands) for a symbol at the current moment to the
// indicators table (migrations/001, migrations/006). Wired into every path that already builds a
// live recommendation from fresh bars: recommendationsHandler (GET /recommendations) and the
// background alert-poll loop (pollAlertsOnce, via alerts.Alert.Bars) — GET /alerts itself no
// longer builds live recommendations at all (see currentAlerts), so it has nothing fresh to
// snapshot; the poll loop already covers every watchlist symbol on its own cadence. Deliberately
// NOT wired into backtesting — those bars are historical/simulated, and this table represents
// live, as-of-now indicator readings, the same "hypothetical, not real" distinction that already
// keeps backtesting's individual simulated recommendations out of the recommendations table (see
// docs/backtesting.md).
func (a *Application) persistIndicatorSnapshot(symbol string, bars []market.Bar) {
	if a.Storage == nil || a.Analyzer == nil {
		return
	}

	points := make([]persistence.IndicatorPoint, 0, 10)
	addLatest := func(indicatorType string, series []float64) {
		if len(series) == 0 {
			return
		}
		points = append(points, persistence.IndicatorPoint{Type: indicatorType, Value: series[len(series)-1]})
	}

	if series, err := a.Analyzer.ComputeRSI(symbol, bars); err == nil {
		addLatest("rsi", series)
	}
	if series, err := a.Analyzer.ComputeEMA(symbol, bars); err == nil {
		addLatest("ema", series)
	}
	if series, err := a.Analyzer.ComputeMACD(symbol, bars); err == nil {
		addLatest("macd", series)
	}
	if series, err := a.Analyzer.ComputeATR(symbol, bars); err == nil {
		addLatest("atr", series)
	}
	if series, err := a.Analyzer.ComputeVolume(symbol, bars); err == nil {
		addLatest("volume", series)
	}
	if series, err := a.Analyzer.ComputeOBV(symbol, bars); err == nil {
		addLatest("obv", series)
	}
	if series, err := a.Analyzer.ComputeADX(symbol, bars); err == nil {
		addLatest("adx", series)
	}
	if bands, err := a.Analyzer.ComputeBollingerBands(symbol, bars); err == nil {
		addLatest("bollinger_upper", bands.Upper)
		addLatest("bollinger_middle", bands.Middle)
		addLatest("bollinger_lower", bands.Lower)
	}

	if err := a.Storage.SaveIndicatorSnapshot(symbol, time.Now().UTC(), points); err != nil {
		a.Logger.Error("failed to persist indicator snapshot", zap.String("symbol", symbol), zap.Error(err))
	}
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

// symbolsHandler lists the symbols currently tracked on the watchlist.
//
// @Summary List watchlist symbols
// @Description Returns every ticker currently on the watchlist, as a plain string list.
// @Tags Market Data
// @Produce json
// @Success 200 {object} map[string][]string "{"symbols": ["AAPL", "MSFT"]}"
// @Failure 500 {object} map[string]string
// @Router /symbols [get]
func (a *Application) symbolsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	items, err := a.Watchlist.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	symbols := make([]string, 0, len(items))
	for _, item := range items {
		symbols = append(symbols, item.Symbol)
	}
	writeJSON(w, http.StatusOK, map[string]any{"symbols": symbols})
}

// pricesHandler collects and persists OHLCV bars for a symbol via the market data provider.
//
// @Summary Get OHLCV price bars for a symbol
// @Description Collects hourly bars from the market data provider (cached — see collectBars),
// @Description persists them, and returns them oldest-first.
// @Tags Market Data
// @Produce json
// @Param symbol query string true "Ticker, e.g. AAPL"
// @Param window query int false "Lookback in days (default: COLLECTOR_WINDOW_DAYS)"
// @Success 200 {object} map[string]any "{"symbol": "AAPL", "bars": [...]}"
// @Failure 400 {object} map[string]string
// @Failure 502 {object} map[string]string "provider unreachable"
// @Router /prices [get]
func (a *Application) pricesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeError(w, http.StatusBadRequest, errors.New("symbol query parameter is required"))
		return
	}
	bars, err := a.collectBars(symbol, a.windowFromQuery(r))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"symbol": symbol, "bars": bars})
}

// portfolioHandler returns the current position snapshot and risk context (GET) or records a
// transaction (POST).
//
// @Summary Get portfolio positions and risk, record a transaction, or remove a position
// @Description GET returns every current position plus the portfolio-wide risk snapshot.
// @Description POST records a BUY/SELL transaction (JSON body: portfolio.Transaction — symbol,
// @Description type, quantity, price, currency are required; PLN needs no currency conversion
// @Description setup) and updates the position; a transaction that sells a position down to zero
// @Description removes it from the position list, keeping the transaction in history.
// @Description DELETE (?symbol=) clears a symbol's current position aggregate without touching
// @Description transaction history — distinct from selling it via a POST.
// @Tags Portfolio
// @Accept json
// @Produce json
// @Param symbol query string false "Ticker to remove (DELETE only)"
// @Param transaction body portfolio.Transaction false "Transaction to record (POST only)"
// @Success 200 {object} map[string]any "GET: {"positions": [...], "risk": {...}}; DELETE: {"status": "removed"}"
// @Success 201 {object} map[string]string "POST: {"status": "recorded"}"
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /portfolio [get]
// @Router /portfolio [post]
// @Router /portfolio [delete]
func (a *Application) portfolioHandler(w http.ResponseWriter, r *http.Request) {
	if err := a.syncActivePortfolio(r); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		positions, err := a.Portfolio.GetPositions()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		risk, err := a.Portfolio.ComputeRisk()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"positions": positions, "risk": risk})
	case http.MethodPost:
		var tx portfolio.Transaction
		if err := json.NewDecoder(r.Body).Decode(&tx); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := a.Portfolio.RecordTransaction(tx); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		go a.refreshRecommendation(tx.Symbol)
		writeJSON(w, http.StatusCreated, map[string]string{"status": "recorded"})
	case http.MethodDelete:
		symbol := r.URL.Query().Get("symbol")
		if symbol == "" {
			writeError(w, http.StatusBadRequest, errors.New("symbol query parameter is required"))
			return
		}
		if err := a.Portfolio.RemovePosition(symbol); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		go a.refreshRecommendation(symbol)
		writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
	default:
		methodNotAllowed(w)
	}
}

// transactionsHandler lists every recorded transaction (GET) or removes one by id (DELETE),
// recomputing the affected symbol's position from what remains (see
// portfolio.UseCase.RemoveTransaction). Distinct from portfolioHandler's DELETE, which clears a
// symbol's current position aggregate without touching transaction history.
//
// @Summary List transaction history, or delete one and recompute its position
// @Description GET returns every recorded transaction. DELETE (?id=) removes one and replays the
// @Description affected symbol's remaining transactions to recompute its position from scratch.
// @Tags Portfolio
// @Produce json
// @Param id query string false "Transaction id to remove (DELETE only)"
// @Success 200 {object} map[string]any "GET: {"transactions": [...]}; DELETE: {"status": "removed"}"
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /transactions [get]
// @Router /transactions [delete]
func (a *Application) transactionsHandler(w http.ResponseWriter, r *http.Request) {
	if err := a.syncActivePortfolio(r); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		transactions, err := a.Portfolio.GetTransactions()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"transactions": transactions})
	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			writeError(w, http.StatusBadRequest, errors.New("id query parameter is required"))
			return
		}
		symbol := a.transactionSymbol(id)
		if err := a.Portfolio.RemoveTransaction(id); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if symbol != "" {
			go a.refreshRecommendation(symbol)
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
	default:
		methodNotAllowed(w)
	}
}

// watchlistHandler lists tracked symbols (GET), adds a new one (POST), or removes one (DELETE).
//
// @Summary List, add, or remove watchlist symbols
// @Description GET returns every tracked item. POST adds a symbol (JSON body: {"symbol": "AAPL"}).
// @Description DELETE (?symbol=) untracks one. Adding an already-tracked symbol, or removing an
// @Description untracked one, is a no-op rather than an error.
// @Tags Watchlist
// @Accept json
// @Produce json
// @Param symbol query string false "Ticker to remove (DELETE only)"
// @Success 200 {object} map[string]any "GET: {"items": [...]}; DELETE: {"status": "removed"}"
// @Success 201 {object} map[string]string "POST: {"status": "added"}"
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /watchlist [get]
// @Router /watchlist [post]
// @Router /watchlist [delete]
func (a *Application) watchlistHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := a.Watchlist.List()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var payload struct {
			Symbol string `json:"symbol"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err := a.Watchlist.Add(payload.Symbol, time.Now().UTC().Format(time.RFC3339)); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"status": "added"})
	case http.MethodDelete:
		symbol := r.URL.Query().Get("symbol")
		if symbol == "" {
			writeError(w, http.StatusBadRequest, errors.New("symbol query parameter is required"))
			return
		}
		if err := a.Watchlist.Remove(symbol); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
	default:
		methodNotAllowed(w)
	}
}

// alertsHandler returns every non-HOLD recommendation for the current watchlist, read from what
// the background alert-poll loop already computed (see currentAlerts) rather than re-evaluating
// live on every request.
//
// @Summary List current actionable alerts
// @Description Every watchlist symbol whose latest recommendation isn't HOLD, as of the last
// @Description background alert-poll cycle (ALERT_POLL_INTERVAL_SECONDS, default 300s) — not
// @Description recomputed live on this request.
// @Tags Recommendations
// @Produce json
// @Success 200 {object} map[string]any "{"alerts": [...]}"
// @Router /alerts [get]
func (a *Application) alertsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	result, err := a.currentAlerts()
	response := map[string]any{"alerts": result}
	if err != nil {
		response["warnings"] = err.Error()
	}
	writeJSON(w, http.StatusOK, response)
}

// currentAlerts reads the Alerts page/API's data from persisted state (see
// persistence.TimescaleDB.LatestRecommendations) instead of re-running the full technical+ML+
// portfolio+risk pipeline for every watchlist symbol on every request — GET /alerts and GET
// /dashboard/alerts used to both call alerts.UseCase.Evaluate directly, the same "recompute the
// whole watchlist to render one page" shape the dashboard's alert-count card had before it was
// fixed to read persisted state too (see web_handlers.go's alertCountsByAction); with the bars
// cache and the alert-poll interval both defaulting to 300s, a request landing in the gap paid
// for up to 47 sequential provider fetches plus ML inference inline. Freshness here is exactly
// "as of the last alert poll" (see AlertPollStatus, surfaced separately on the Alerts page),
// same tradeoff already accepted for the dashboard card.
//
// Scoped to the current watchlist, same convention as alertCountsByAction/countActionable, so a
// symbol removed from the watchlist stops appearing even if its last recorded action was never
// overwritten.
func (a *Application) currentAlerts() ([]alerts.Alert, error) {
	if a.Storage == nil {
		return nil, nil
	}
	recommendations, err := a.Storage.LatestRecommendations()
	if err != nil {
		return nil, err
	}
	items, err := a.Watchlist.List()
	if err != nil {
		return nil, err
	}
	return filterCurrentAlerts(recommendations, items), nil
}

// filterCurrentAlerts applies the two rules that can silently put a wrong alert on screen — skip
// HOLD, and only ever show a symbol still on the watchlist — split out from currentAlerts purely
// so they're testable without a database behind them, same reasoning as countActionable above.
func filterCurrentAlerts(recommendations map[string]persistence.LatestRecommendation, items []watchlist.Item) []alerts.Alert {
	result := make([]alerts.Alert, 0, len(items))
	for _, item := range items {
		latest, ok := recommendations[item.Symbol]
		if !ok || latest.Action == "HOLD" {
			continue
		}
		result = append(result, alerts.Alert{
			Recommendation: latest.Recommendation,
			TriggeredAt:    latest.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return result
}

// recommendationsHandler builds an explainable BUY/SELL/HOLD recommendation for a symbol.
//
// @Summary Get a live BUY/SELL/HOLD recommendation for a symbol
// @Description Runs the full technical+ML+portfolio+risk pipeline against freshly collected
// @Description bars and returns an explainable decision.Recommendation. Decision-support only —
// @Description Atlas never executes trades.
// @Tags Recommendations
// @Produce json
// @Param symbol query string true "Ticker, e.g. AAPL"
// @Param window query int false "Lookback in days (default: COLLECTOR_WINDOW_DAYS)"
// @Success 200 {object} decision.Recommendation
// @Failure 400 {object} map[string]string
// @Failure 502 {object} map[string]string "provider unreachable"
// @Router /recommendations [get]
func (a *Application) recommendationsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeError(w, http.StatusBadRequest, errors.New("symbol query parameter is required"))
		return
	}
	bars, err := a.collectBars(symbol, a.windowFromQuery(r))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	recommendation, err := a.Orchestrator.BuildRecommendationFromBars(symbol, bars)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	a.Metrics.RecommendationsTotal.WithLabelValues(recommendation.Action).Inc()
	a.persistRecommendation(recommendation)
	a.persistIndicatorSnapshot(symbol, bars)
	writeJSON(w, http.StatusOK, recommendation)
}

// predictionsHandler runs the ML inference path for a symbol using freshly collected bars.
// @Summary Get a raw ML prediction for a symbol
// @Description Runs the ML inference path alone (no technical/portfolio/risk blend, no
// @Description BUY/SELL/HOLD decision) against freshly collected bars — the deterministic
// @Description RSI-derived stub when no trained model is loaded (see internal/application/ml).
// @Tags Machine Learning
// @Produce json
// @Param symbol query string true "Ticker, e.g. AAPL"
// @Param window query int false "Lookback in days (default: COLLECTOR_WINDOW_DAYS)"
// @Success 200 {object} mlDomain.Prediction
// @Failure 400 {object} map[string]string
// @Failure 502 {object} map[string]string "provider unreachable"
// @Router /predictions [get]
func (a *Application) predictionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	symbol := r.URL.Query().Get("symbol")
	if symbol == "" {
		writeError(w, http.StatusBadRequest, errors.New("symbol query parameter is required"))
		return
	}
	bars, err := a.collectBars(symbol, a.windowFromQuery(r))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	feature := mlUC.BuildFeatureVector(a.Analyzer, symbol, bars)
	prediction, err := a.ML.Predict(feature)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	prediction.Symbol = symbol
	a.Metrics.PredictionsTotal.WithLabelValues(prediction.ModelVersion).Inc()
	a.persistPrediction(prediction)
	writeJSON(w, http.StatusOK, prediction)
}

// backtestWindow is the default lookback for /backtests: long enough to produce a meaningful
// simulation, well beyond the shorter default used for a single live recommendation.
const backtestDefaultWindow = 180
const backtestDefaultCapital = 10000.0

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

	bars, err := a.collectBars(symbol, window)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	result, err := a.Backtesting.Run(symbol, bars, capital, strategyName, strategyVersion)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	a.persistBacktestRun(result)
	writeJSON(w, http.StatusOK, result)
}

// backtestWalkForwardDefaultWindow is wider than backtestDefaultWindow: RunWalkForward splits
// the window 60/20/20 (train/validate/test — see backtesting.splitWalkForward), and each split
// independently needs enough bars past its own indicator warm-up to be meaningful, not just to
// clear the minimum.
const backtestWalkForwardDefaultWindow = 500

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

	bars, err := a.collectBars(symbol, window)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	result, err := a.Backtesting.RunWalkForward(symbol, bars, capital, strategyName, strategyVersion)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

const backtestHistoryDefaultLimit = 20

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

// maxWatchlistImportBytes caps an import request body. A watchlist of a few dozen symbols is a
// couple of kilobytes; this leaves several orders of magnitude of headroom while stopping a
// malformed or hostile upload from being read into memory unbounded.
const maxWatchlistImportBytes = 1 << 20 // 1 MiB

// maxBackupRestoreBytes caps an uploaded accountRestoreHandler file. A full BackupAll dump
// (prices, indicators, recommendations included) is real bulk data, not a handful of symbols
// like the watchlist import above, so this leaves generous headroom for that while still
// stopping an unbounded read into memory.
const maxBackupRestoreBytes = 200 << 20 // 200 MiB

// watchlistExportHandler serves the watchlist as a downloadable JSON file, in exactly the shape
// GET /watchlist already returns and watchlistImportHandler already accepts — so an export from
// one Atlas instance imports into another with no conversion step, which is the entire point of
// the feature (moving a real watchlist between environments beats re-entering it by hand).
// @Summary Download the watchlist as a JSON file
// @Description Same shape GET /watchlist returns and POST /watchlist/import accepts — an export
// @Description from one Atlas instance imports into another with no conversion step.
// @Tags Watchlist
// @Produce json
// @Success 200 {object} map[string]any "{"items": [...]}"
// @Router /watchlist/export [get]
func (a *Application) watchlistExportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	items, err := a.Watchlist.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if items == nil {
		items = []watchlist.Item{} // an empty watchlist exports as [], never as null
	}
	filename := fmt.Sprintf("atlas-watchlist-%s.json", time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// decodeWatchlistImport accepts either the full export shape or a bare list of symbols:
//
//	{"items":[{"symbol":"AAPL","added_at":"..."}]}
//	["AAPL","MSFT"]
//
// The first round-trips an export exactly; the second exists because a hand-written list of
// tickers is the other realistic way someone produces this file, and refusing it would mean
// hand-wrapping every symbol in an object for no benefit.
func decodeWatchlistImport(body []byte) ([]watchlist.Item, error) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return nil, errors.New("import body is empty")
	}

	if trimmed[0] == '[' {
		var symbols []string
		if err := json.Unmarshal(trimmed, &symbols); err != nil {
			return nil, fmt.Errorf("parse symbol list: %w", err)
		}
		items := make([]watchlist.Item, 0, len(symbols))
		for _, symbol := range symbols {
			items = append(items, watchlist.Item{Symbol: symbol})
		}
		return items, nil
	}

	var payload struct {
		Items []watchlist.Item `json:"items"`
	}
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return nil, fmt.Errorf("parse watchlist export: %w", err)
	}
	if payload.Items == nil {
		return nil, errors.New(`expected {"items":[...]} or a JSON array of symbols`)
	}
	return payload.Items, nil
}

// watchlistImportHandler applies an exported watchlist. ?mode=replace mirrors the file exactly
// (untracking whatever it omits); anything else, including an absent parameter, merges — the
// destructive mode always has to be asked for by name.
// @Summary Import a watchlist export
// @Description Accepts either the full export shape ({"items":[...]}) or a bare JSON array of
// @Description symbols. mode=merge (default) keeps existing symbols; mode=replace untracks
// @Description whatever the file omits.
// @Tags Watchlist
// @Accept json
// @Produce json
// @Param mode query string false "merge (default) or replace"
// @Param items body object true "Watchlist export body — see description"
// @Success 200 {object} watchlistUC.ImportResult
// @Failure 400 {object} map[string]string
// @Failure 413 {object} map[string]string "body over 1 MiB"
// @Router /watchlist/import [post]
func (a *Application) watchlistImportHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	mode, err := watchlistUC.ParseImportMode(r.URL.Query().Get("mode"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWatchlistImportBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, err)
		return
	}
	items, err := decodeWatchlistImport(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	result, err := a.Watchlist.Import(items, mode, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
