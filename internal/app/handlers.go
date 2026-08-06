package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	mlUC "atlas/internal/application/ml"
	"atlas/internal/domain/market"
	"atlas/internal/domain/portfolio"

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

// portfolioHandler returns the current position snapshot (GET) or records a transaction (POST).
func (a *Application) portfolioHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		positions, err := a.Portfolio.GetPositions()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"positions": positions})
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
	default:
		methodNotAllowed(w)
	}
}

// watchlistHandler lists tracked symbols (GET) or adds a new one (POST).
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
	writeJSON(w, http.StatusOK, result)
}
