package app

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"atlas/internal/app/web"
	"atlas/internal/domain/alerts"
	"atlas/internal/domain/watchlist"
	"atlas/internal/infrastructure/persistence"

	"go.uber.org/zap"
)

// The read-only analysis pages — recommendations, alerts and backtests — plus the dashboard's
// alert summary. Split out of web_handlers.go by feature.

// recommendationsPageHandler mirrors recommendationsHandler (GET /recommendations) — same
// orchestrator call, same persistence side effects (recommendation + indicator snapshot) — just
// rendered as HTML instead of JSON.
func (a *Application) recommendationsPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	symbol := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))
	data := web.RecommendationsPage{Lang: a.resolveLang(r), Symbol: symbol}
	if symbols, err := a.watchlistSymbols(); err == nil {
		data.Symbols = symbols
	}
	if symbol != "" {
		bars, err := a.collectBars(symbol, a.windowFromQuery(r))
		if err != nil {
			data.Error = err.Error()
		} else if rec, err := a.Orchestrator.BuildRecommendationFromBars(symbol, bars); err != nil {
			data.Error = err.Error()
		} else {
			a.Metrics.RecommendationsTotal.WithLabelValues(rec.Action).Inc()
			a.persistRecommendation(rec)
			a.persistIndicatorSnapshot(symbol, bars)
			data.Recommendation = &rec
		}
	}
	web.Render(w, "recommendations", data)
}

// alertsPageHandler mirrors alertsHandler (GET /alerts) — same data source (currentAlerts).
func (a *Application) alertsPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	result, err := a.currentAlerts()
	lang := a.resolveLang(r)
	data := web.AlertsPage{Lang: lang, Alerts: result, ActiveFor: a.activeAlertDurations(lang, result), Info: a.loadSymbolInfo()}
	if err != nil {
		data.Warning = err.Error()
	}
	data.PollAt, data.PollError = a.AlertPoll.Snapshot()
	web.Render(w, "alerts", data)
}

// activeAlertDurations reports, per alerted symbol, how long it has held its current recommended
// action — the one thing the alerts themselves cannot say, since every recommendation is stamped
// with the moment it was computed rather than the moment the signal appeared.
//
// The streak is only reported when the stored action still matches the action currentAlerts just
// read. Both now come from the same recommendations table (currentAlerts's LatestRecommendations,
// this function's CurrentActionStreaks), so the two disagreeing takes a poll writing between the
// two queries — rare, but the guard costs nothing and a wrong duration next to the wrong action
// would be worse than a blank cell.
//
// Best-effort by design: no storage, or a failed query, costs the duration column and nothing
// else. Alerts are the point of the page and must still render.
func (a *Application) activeAlertDurations(lang web.Lang, result []alerts.Alert) map[string]web.ActiveDuration {
	if a.Storage == nil || len(result) == 0 {
		return nil
	}
	streaks, err := a.Storage.CurrentActionStreaks()
	if err != nil {
		a.Logger.Warn("failed to load recommendation streaks for the alerts page", zap.Error(err))
		return nil
	}

	now := time.Now().UTC()
	durations := make(map[string]web.ActiveDuration, len(result))
	for _, alert := range result {
		streak, found := streaks[alert.Recommendation.Symbol]
		if !found || streak.Action != alert.Recommendation.Action {
			continue
		}
		durations[alert.Recommendation.Symbol] = web.FormatActiveDuration(lang, streak.Since.UTC(), now)
	}
	return durations
}

// backtestsPageHandler mirrors backtestsHandler (GET /backtests) — same simulation, same
// persistence, fixed to the "atlas-orchestrator"/"v1" strategy tag (the dashboard form has no
// use for the strategy-name/version query params the JSON API exposes for scripting).
func (a *Application) backtestsPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	symbol := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))
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

	data := web.BacktestsPage{Lang: a.resolveLang(r), Symbol: symbol, Window: window, Capital: capital}
	if symbols, err := a.watchlistSymbols(); err == nil {
		data.Symbols = symbols
	}
	if symbol != "" {
		bars, err := a.collectBars(symbol, window)
		if err != nil {
			data.Error = err.Error()
		} else if result, err := a.Backtesting.WithContext(r.Context()).Run(symbol, bars, capital, "atlas-orchestrator", "v1"); err != nil {
			data.Error = err.Error()
		} else {
			a.persistBacktestRun(result)
			data.Result = &result
		}
	}
	web.Render(w, "backtests", data)
}

// alertCountsByAction gives the dashboard's "Active Alerts" summary card a count per action
// (e.g. {"BUY": 2, "SELL": 1}), read from the actions the alert poll loop last recorded.
//
// It used to call Alerts.Evaluate, which meant every dashboard render collected bars for all 47
// watchlist symbols sequentially and ran the full orchestrator, ML inference included. With the
// 300s bars cache warm that cost about 0.18s; cold — after a restart, or once the TTL lapsed —
// it took 5 to 10 seconds. Measured over 24h of live traffic before this changed, "/" ran p50
// 0.183s but p90 8.4s, with 14 of 66 renders past 5s. That bimodality is exactly the "sometimes
// the dashboard hangs" the Author reported. The replacement is one indexed query, 7ms.
//
// The counts are now as of the last alert poll rather than computed on the spot. For a summary
// card that is the better number anyway: it agrees with the notification actually sent, instead
// of being a second, independently-computed opinion that could contradict it minutes later.
// Scoped to the current watchlist so a removed symbol's last recorded action stops being counted.
func (a *Application) alertCountsByAction() map[string]int {
	if a.Storage == nil {
		return nil
	}
	streaks, err := a.Storage.CurrentActionStreaks()
	if err != nil {
		a.Logger.Warn("dashboard alert summary: could not read recorded actions", zap.Error(err))
		return nil
	}
	items, err := a.Watchlist.List()
	if err != nil {
		a.Logger.Warn("dashboard alert summary: could not read the watchlist", zap.Error(err))
		return nil
	}

	return countActionable(streaks, items)
}

// countActionable tallies each watchlist symbol's currently-recorded action, skipping HOLD.
// Split out from alertCountsByAction purely so the two rules that can silently put a wrong
// number on screen — skip HOLD, and count only symbols still on the watchlist — are testable
// without a database behind them.
func countActionable(streaks map[string]persistence.ActionStreak, items []watchlist.Item) map[string]int {
	counts := make(map[string]int)
	for _, item := range items {
		if streak, ok := streaks[item.Symbol]; ok && streak.Action != "HOLD" {
			counts[streak.Action]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}
