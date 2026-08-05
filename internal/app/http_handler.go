package app

import (
	"encoding/json"
	"net/http"
	"time"

	"atlas/internal/domain"
)

func (a *Application) NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", a.Metrics.Handler())
	mux.HandleFunc("/health", a.healthHandler)
	mux.HandleFunc("/symbols", a.symbolsHandler)
	mux.HandleFunc("/prices", a.pricesHandler)
	mux.HandleFunc("/portfolio", a.portfolioHandler)
	mux.HandleFunc("/watchlist", a.watchlistHandler)
	mux.HandleFunc("/alerts", a.alertsHandler)
	mux.HandleFunc("/recommendations", a.recommendationsHandler)
	mux.HandleFunc("/predictions", a.predictionsHandler)
	mux.HandleFunc("/backtests", a.backtestsHandler)
	mux.HandleFunc("/", a.rootHandler)
	return a.instrument(mux)
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

func (a *Application) healthHandler(w http.ResponseWriter, r *http.Request) {
	status := domain.HealthStatus{
		Status:   "healthy",
		Service:  "atlas-api",
		Database: "unknown",
		Redis:    "unknown",
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func (a *Application) rootHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("Atlas API ready\n"))
}
