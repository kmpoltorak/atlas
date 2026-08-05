package app

import (
	"encoding/json"
	"net/http"

	"atlas/internal/domain"
)

func (a *Application) NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", a.Metrics.Handler())
	mux.HandleFunc("/health", a.healthHandler)
	mux.HandleFunc("/", a.rootHandler)
	return mux
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
