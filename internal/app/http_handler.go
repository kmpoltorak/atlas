package app

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"atlas/internal/app/web"
	"atlas/internal/domain"

	httpSwagger "github.com/swaggo/http-swagger/v2"

	_ "atlas/docs" // registers the generated OpenAPI spec with swaggo/swag — see docs/api.md
)

// scoped adapts a handler method so it runs against a request-scoped Application — one whose
// storage handle is cancelled with the request (see forRequest). Applied at registration rather
// than inside each handler: it is the same wrapping every time, and doing it here means a new
// route cannot forget it.
func (a *Application) scoped(handler func(*Application, http.ResponseWriter, *http.Request)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		handler(a.forRequest(r), w, r)
	}
}

func (a *Application) NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", a.Metrics.Handler())
	mux.Handle("/static/", http.FileServer(http.FS(web.StaticFS)))
	// /swagger/index.html — interactive API docs (see cmd/api/main.go's @title block and the
	// @Summary/@Router annotations on each JSON handler below).
	mux.HandleFunc("/swagger/", httpSwagger.WrapHandler)
	mux.HandleFunc("/health", a.scoped((*Application).healthHandler))
	mux.HandleFunc("GET /live", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("/symbols", a.scoped((*Application).symbolsHandler))
	mux.HandleFunc("/prices", a.scoped((*Application).pricesHandler))
	mux.HandleFunc("/portfolio", a.scoped((*Application).portfolioHandler))
	mux.HandleFunc("/transactions", a.scoped((*Application).transactionsHandler))
	mux.HandleFunc("/watchlist", a.scoped((*Application).watchlistHandler))
	mux.HandleFunc("/watchlist/export", a.scoped((*Application).watchlistExportHandler))
	mux.HandleFunc("/watchlist/import", a.scoped((*Application).watchlistImportHandler))
	mux.HandleFunc("/alerts", a.scoped((*Application).alertsHandler))
	mux.HandleFunc("/recommendations", a.scoped((*Application).recommendationsHandler))
	mux.HandleFunc("/predictions", a.scoped((*Application).predictionsHandler))
	mux.HandleFunc("/backtests", a.scoped((*Application).backtestsHandler))
	mux.HandleFunc("/backtests/history", a.scoped((*Application).backtestHistoryHandler))
	mux.HandleFunc("/backtests/walkforward", a.scoped((*Application).backtestWalkForwardHandler))

	// Server-rendered dashboard (see internal/app/web).
	mux.HandleFunc("/dashboard/lang", a.scoped((*Application).langSetHandler))
	mux.HandleFunc("/dashboard/portfolio", a.scoped((*Application).portfolioPageHandler))
	// One route per thing being deleted, not one per selection shape: each handler takes either
	// the checkbox-select bulk field or a single per-row value (see web_handlers.go's formValues).
	mux.HandleFunc("/dashboard/portfolio/delete", a.scoped((*Application).portfolioDeleteHandler))
	mux.HandleFunc("/dashboard/portfolio/transactions/delete", a.scoped((*Application).portfolioTransactionDeleteHandler))
	mux.HandleFunc("/dashboard/portfolio/select", a.scoped((*Application).portfolioSelectHandler))
	mux.HandleFunc("/dashboard/portfolios/create", a.scoped((*Application).portfolioCreateHandler))
	mux.HandleFunc("/dashboard/portfolios/rename", a.scoped((*Application).portfolioRenameHandler))
	mux.HandleFunc("/dashboard/portfolios/remove", a.scoped((*Application).portfolioRemoveHandler))
	mux.HandleFunc("/dashboard/recommendations", a.scoped((*Application).recommendationsPageHandler))
	mux.HandleFunc("/dashboard/alerts", a.scoped((*Application).alertsPageHandler))
	mux.HandleFunc("/dashboard/backtests", a.scoped((*Application).backtestsPageHandler))
	mux.HandleFunc("/dashboard/watchlist", a.scoped((*Application).watchlistPageHandler))
	mux.HandleFunc("/dashboard/watchlist/delete", a.scoped((*Application).watchlistDeleteHandler))
	mux.HandleFunc("/dashboard/watchlist/import", a.scoped((*Application).watchlistImportPageHandler))
	mux.HandleFunc("/dashboard/watchlist/refresh-info", a.scoped((*Application).watchlistRefreshInfoHandler))
	mux.HandleFunc("/dashboard/account", a.scoped((*Application).accountPageHandler))
	mux.HandleFunc("/dashboard/account/ml/toggle", a.scoped((*Application).mlEngineToggleHandler))
	mux.HandleFunc("/dashboard/account/ml/activate", a.scoped((*Application).mlModelActivateHandler))
	mux.HandleFunc("/dashboard/account/ml/delete", a.scoped((*Application).mlModelDeleteHandler))
	mux.HandleFunc("/dashboard/account/ml/train", a.scoped((*Application).mlTrainHandler))
	mux.HandleFunc("/dashboard/account/backup", a.scoped((*Application).accountBackupHandler))
	mux.HandleFunc("/dashboard/account/restore", a.scoped((*Application).accountRestoreHandler))
	mux.HandleFunc("/dashboard/account/flush", a.scoped((*Application).accountFlushHandler))

	// Bare "/dashboard" (no trailing path) isn't a real route — every actual page lives under
	// "/dashboard/..." — but it's a natural URL to type or bookmark expecting the dashboard
	// itself, and used to silently work as an alias for "/" before "/" got a real 404 for
	// anything unmatched (see dashboardPageHandler's own comment below). A redirect is cheap and
	// avoids turning a plausible bookmark into a dead link.
	mux.HandleFunc("/dashboard", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	})
	// Every browser requests this on its own, independent of the <link rel="icon"> partials.html
	// already sets in <head> — 204 here is cheap and keeps it from showing up as a real 404 in
	// metrics/logs for a path that was never meant to be a route.
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	// "/" is the dashboard itself (not a separate "/dashboard" route — a subtree pattern in
	// net/http.ServeMux, so this is also the catch-all for anything else unmatched above).
	mux.HandleFunc("/", a.scoped((*Application).dashboardPageHandler))
	// No login: Atlas is a local, single-operator tool whose ports are bound to 127.0.0.1 (see
	// docker-compose.yml). Cross-origin protection stays — it is what stops a web page open in
	// the same browser from posting to the dashboard, login or not.
	return a.instrument(http.NewCrossOriginProtection().Handler(mux))
}

// instrument records request count and latency for every route except /metrics itself,
// so the counters the observability package already registers are actually populated.
func (a *Application) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/metrics" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)

		route := r.Pattern
		if route == "" || (route == "/" && r.URL.Path != "/") {
			route = "unmatched"
		}
		method := r.Method
		switch method {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		default:
			method = "OTHER"
		}
		a.Metrics.RequestCounter.WithLabelValues(route, method, http.StatusText(recorder.status)).Inc()
		a.Metrics.RequestDuration.WithLabelValues(route, method).Observe(time.Since(start).Seconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

// Unwrap exposes the underlying ResponseWriter to http.ResponseController — without it,
// SetWriteDeadline (accountBackupHandler/accountRestoreHandler/accountFlushHandler's
// extendWriteDeadline) fails with "feature not supported" on every request, since instrument
// wraps every ResponseWriter in a *statusRecorder that the standard library otherwise has no way
// to see through to the real connection. Confirmed live: this was silently no-op-ing before.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

// healthHandler pings the real database and cache connections, per the response shape
// documented in docs/deployment.md ({"status":"healthy","database":"ok","redis":"ok"}).
// Returns 503 when either dependency is unreachable, so this is a meaningful signal for
// the Docker healthcheck (docker-compose.yml), not just a liveness check.
// Trainer reachability is reported too when TRAINER_URL is configured, but — unlike
// Database/Redis — never flips the overall status/HTTP code: the trainer is a genuinely
// optional sidecar (TRAINER_URL empty hides it) atlas-api already degrades
// gracefully without (the Account page's "Train new candidate" button just explains the manual
// path instead), so Docker restarting atlas-api because atlas-trainer is down would
// be exactly the wrong response.
//
// @Summary Health check
// @Description Pings the database and cache directly (not a bare liveness check). No
// @Description authentication required — see auth_middleware.go's publicPaths.
// @Tags System
// @Produce json
// @Success 200 {object} domain.HealthStatus
// @Failure 503 {object} domain.HealthStatus "database or redis unreachable"
// @Router /health [get]
func (a *Application) healthHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	statuses := a.dependencyStatuses(ctx)
	toStatus := func(key string) string {
		up, checked := statuses[key]
		switch {
		case !checked:
			return ""
		case up:
			return "ok"
		default:
			return "error"
		}
	}

	overallStatus := "healthy"
	httpStatus := http.StatusOK
	if !statuses["database"] || !statuses["redis"] {
		overallStatus = "unhealthy"
		httpStatus = http.StatusServiceUnavailable
	}

	status := domain.HealthStatus{
		Status:       overallStatus,
		Service:      "atlas-api",
		Database:     toStatus("database"),
		Redis:        toStatus("redis"),
		Trainer:      toStatus("trainer"),
		Prometheus:   toStatus("prometheus"),
		Alertmanager: toStatus("alertmanager"),
		Grafana:      toStatus("grafana"),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(status)
}

// pingHTTPStatus GETs baseURL+path and reports "ok"/"error", or "" when baseURL is empty (that
// check is disabled — see Config.PrometheusURL's doc comment). A plain http.Client is enough
// here: unlike Storage/Cache/Trainer, there's no existing client type for these three, and a
// one-line GET-and-check-200 is all /health needs from any of them.
func pingHTTPStatus(ctx context.Context, baseURL, path string) string {
	if baseURL == "" {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return "error"
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "error"
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "error"
	}
	return "ok"
}
