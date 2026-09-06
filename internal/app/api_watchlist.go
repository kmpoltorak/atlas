package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	watchlistUC "atlas/internal/application/watchlist"
	"atlas/internal/domain/alerts"
	"atlas/internal/domain/watchlist"
	"atlas/internal/infrastructure/persistence"
)

// JSON API for the watchlist and the alerts derived from it, including CSV/JSON import and
// export. Split out of handlers.go by resource.

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
