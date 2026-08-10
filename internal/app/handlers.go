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
			zap.String("provider", a.Config.MarketProvider),
			zap.String("symbol", symbol),
			zap.Error(err),
		)
	}
	a.Metrics.ProviderRequests.WithLabelValues(a.Config.MarketProvider, outcome).Inc()
	return bars, err
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
// alert-poll loop / alertsHandler (GET /alerts) via alerts.Alert.Bars. Deliberately NOT wired into
// backtesting — those bars are historical/simulated, and this table represents live, as-of-now
// indicator readings, the same "hypothetical, not real" distinction that already keeps
// backtesting's individual simulated recommendations out of the recommendations table (see
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
func (a *Application) portfolioHandler(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
	default:
		methodNotAllowed(w)
	}
}

// transactionsHandler lists every recorded transaction (GET) or removes one by id (DELETE),
// recomputing the affected symbol's position from what remains (see
// portfolio.UseCase.RemoveTransaction). Distinct from portfolioHandler's DELETE, which clears a
// symbol's current position aggregate without touching transaction history.
func (a *Application) transactionsHandler(w http.ResponseWriter, r *http.Request) {
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
		if err := a.Portfolio.RemoveTransaction(id); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
	default:
		methodNotAllowed(w)
	}
}

// watchlistHandler lists tracked symbols (GET), adds a new one (POST), or removes one (DELETE).
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

// alertsHandler evaluates the watchlist and returns every non-HOLD recommendation. Per-symbol
// collection failures are surfaced as warnings rather than failing the whole request.
func (a *Application) alertsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	result, err := a.Alerts.Evaluate()
	for _, alert := range result {
		a.persistRecommendation(alert.Recommendation)
		a.persistIndicatorSnapshot(alert.Recommendation.Symbol, alert.Bars)
	}
	response := map[string]any{"alerts": result}
	if err != nil {
		response["warnings"] = err.Error()
	}
	writeJSON(w, http.StatusOK, response)
}

// recommendationsHandler builds an explainable BUY/SELL/HOLD recommendation for a symbol.
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

// watchlistExportHandler serves the watchlist as a downloadable JSON file, in exactly the shape
// GET /watchlist already returns and watchlistImportHandler already accepts — so an export from
// one Atlas instance imports into another with no conversion step, which is the entire point of
// the feature (moving a real watchlist between environments beats re-entering it by hand).
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
