package app

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"atlas/internal/app/web"
	"atlas/internal/domain"
)

func (a *Application) NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", a.Metrics.Handler())
	mux.Handle("/static/", http.FileServer(http.FS(web.StaticFS)))
	mux.HandleFunc("/health", a.healthHandler)
	mux.HandleFunc("/symbols", a.symbolsHandler)
	mux.HandleFunc("/prices", a.pricesHandler)
	mux.HandleFunc("/portfolio", a.portfolioHandler)
	mux.HandleFunc("/transactions", a.transactionsHandler)
	mux.HandleFunc("/watchlist", a.watchlistHandler)
	mux.HandleFunc("/alerts", a.alertsHandler)
	mux.HandleFunc("/recommendations", a.recommendationsHandler)
	mux.HandleFunc("/predictions", a.predictionsHandler)
	mux.HandleFunc("/backtests", a.backtestsHandler)
	mux.HandleFunc("/backtests/history", a.backtestHistoryHandler)
	mux.HandleFunc("/backtests/walkforward", a.backtestWalkForwardHandler)

	// Server-rendered dashboard (Phase 8 — see implementation-plan.md and internal/app/web).
	mux.HandleFunc("/login", a.loginPageHandler)
	mux.HandleFunc("/logout", a.logoutPageHandler)
	mux.HandleFunc("/dashboard/lang", a.langSetHandler)
	mux.HandleFunc("/dashboard/portfolio", a.portfolioPageHandler)
	mux.HandleFunc("/dashboard/portfolio/delete", a.portfolioDeleteHandler)
	mux.HandleFunc("/dashboard/portfolio/bulk-delete", a.portfolioBulkDeleteHandler)
	mux.HandleFunc("/dashboard/portfolio/transactions/delete", a.portfolioTransactionDeleteHandler)
	mux.HandleFunc("/dashboard/portfolio/transactions/bulk-delete", a.portfolioTransactionBulkDeleteHandler)
	mux.HandleFunc("/dashboard/recommendations", a.recommendationsPageHandler)
	mux.HandleFunc("/dashboard/alerts", a.alertsPageHandler)
	mux.HandleFunc("/dashboard/backtests", a.backtestsPageHandler)
	mux.HandleFunc("/dashboard/watchlist", a.watchlistPageHandler)
	mux.HandleFunc("/dashboard/watchlist/delete", a.watchlistDeleteHandler)
	mux.HandleFunc("/dashboard/watchlist/bulk-delete", a.watchlistBulkDeleteHandler)
	mux.HandleFunc("/dashboard/account", a.accountPageHandler)
	mux.HandleFunc("/dashboard/account/totp/setup", a.totpSetupHandler)
	mux.HandleFunc("/dashboard/account/totp/qr.png", a.totpQRHandler)
	mux.HandleFunc("/dashboard/account/totp/confirm", a.totpConfirmHandler)
	mux.HandleFunc("/dashboard/account/totp/disable", a.totpDisableHandler)
	mux.HandleFunc("/dashboard/account/ml/activate", a.mlModelActivateHandler)
	mux.HandleFunc("/dashboard/account/ml/delete", a.mlModelDeleteHandler)
	mux.HandleFunc("/dashboard/account/ml/train", a.mlTrainHandler)

	// "/" is the dashboard itself (not a separate "/dashboard" route — a subtree pattern in
	// net/http.ServeMux, so this is also the catch-all for anything else unmatched above).
	mux.HandleFunc("/", a.dashboardPageHandler)
	return a.instrument(a.requireAuth(mux))
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

		a.Metrics.RequestCounter.WithLabelValues(r.URL.Path, r.Method, http.StatusText(recorder.status)).Inc()
		a.Metrics.RequestDuration.WithLabelValues(r.URL.Path, r.Method).Observe(time.Since(start).Seconds())
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

// healthHandler pings the real database and cache connections, per the response shape
// documented in docs/deployment.md ({"status":"healthy","database":"ok","redis":"ok"}).
// Returns 503 when either dependency is unreachable, so this is a meaningful signal for
// Docker/Kubernetes health probes (see deploy/deployment.yaml), not just a liveness check.
// Trainer reachability is reported too when TRAINER_URL is configured, but — unlike
// Database/Redis — never flips the overall status/HTTP code: the trainer is a genuinely
// optional sidecar (see docker-compose.yml's "trainer" profile) atlas-api already degrades
// gracefully without (the Account page's "Train new candidate" button just explains the manual
// path instead), so Docker/Kubernetes restarting atlas-api because atlas-trainer is down would
// be exactly the wrong response.
func (a *Application) healthHandler(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	databaseStatus := "ok"
	if err := a.Storage.Ping(ctx); err != nil {
		databaseStatus = "error"
	}
	redisStatus := "ok"
	if err := a.Cache.Ping(ctx); err != nil {
		redisStatus = "error"
	}
	var trainerStatus string
	if a.Trainer != nil {
		trainerStatus = "ok"
		if err := a.Trainer.Ping(ctx); err != nil {
			trainerStatus = "error"
		}
	}

	overallStatus := "healthy"
	httpStatus := http.StatusOK
	if databaseStatus != "ok" || redisStatus != "ok" {
		overallStatus = "unhealthy"
		httpStatus = http.StatusServiceUnavailable
	}

	status := domain.HealthStatus{
		Status:   overallStatus,
		Service:  "atlas-api",
		Database: databaseStatus,
		Redis:    redisStatus,
		Trainer:  trainerStatus,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_ = json.NewEncoder(w).Encode(status)
}
