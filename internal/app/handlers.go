// Shared JSON-API plumbing plus the market-data endpoints (symbols, prices, recommendations,
// predictions). The per-resource handlers live alongside in api_portfolio.go, api_watchlist.go
// and api_backtests.go; the server-rendered dashboard is web_*.go.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	collectorUC "atlas/internal/application/collector"
	mlUC "atlas/internal/application/ml"
	"atlas/internal/domain/decision"
	"atlas/internal/domain/market"
	mlDomain "atlas/internal/domain/ml"
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
func (a *Application) collectBars(ctx context.Context, symbol string, window int) ([]market.Bar, error) {
	bars, fromCache, err := a.collectorFor(ctx).Collect(ctx, symbol, window)
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

// collectorFor returns a collector whose database writes are cancelled with ctx, the way every
// other repository a handler touches is scoped (see persistence.TimescaleDB.WithContext). The
// shared collector holds the process-wide storage handle, so without this the bars fetched for a
// request that had already gone away were still written on its behalf.
func (a *Application) collectorFor(ctx context.Context) *collectorUC.UseCase {
	if a.Collector == nil || a.Storage == nil || a.Collector.Storage == nil {
		return a.Collector
	}
	scoped := *a.Collector
	scoped.Storage = a.Storage.WithContext(ctx)
	return &scoped
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
	bars, err := a.collectBars(r.Context(), symbol, a.windowFromQuery(r))
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"symbol": symbol, "bars": bars})
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
	bars, err := a.collectBars(r.Context(), symbol, a.windowFromQuery(r))
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
	bars, err := a.collectBars(r.Context(), symbol, a.windowFromQuery(r))
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

// backtestWalkForwardDefaultWindow is wider than backtestDefaultWindow: RunWalkForward splits
// the window 60/20/20 (train/validate/test — see backtesting.splitWalkForward), and each split
// independently needs enough bars past its own indicator warm-up to be meaningful, not just to
// clear the minimum.
const backtestWalkForwardDefaultWindow = 500

const backtestHistoryDefaultLimit = 20

// maxWatchlistImportBytes caps an import request body. A watchlist of a few dozen symbols is a
// couple of kilobytes; this leaves several orders of magnitude of headroom while stopping a
// malformed or hostile upload from being read into memory unbounded.
const maxWatchlistImportBytes = 1 << 20 // 1 MiB

// maxBackupRestoreBytes caps an uploaded accountRestoreHandler file. A full BackupAll dump
// (prices, indicators, recommendations included) is real bulk data, not a handful of symbols
// like the watchlist import above, so this leaves generous headroom for that while still
// stopping an unbounded read into memory.
const maxBackupRestoreBytes = 200 << 20 // 200 MiB
