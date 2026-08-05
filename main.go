package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	requestCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "atlas_api_requests_total",
			Help: "Total number of HTTP requests handled by Atlas API.",
		},
		[]string{"path", "method", "status"},
	)
	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "atlas_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"path", "method"},
	)
)

func main() {
	registry := prometheus.NewRegistry()
	registry.MustRegister(requestCounter, requestDuration)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/", defaultHandler)

	server := &http.Server{
		Addr:              ":8080",
		Handler:           instrumentedHandler(registry, mux),
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	log.Printf("atlas api listening on %s", server.Addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func instrumentedHandler(registry *prometheus.Registry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		statusCode := http.StatusOK
		wrapped := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		statusCode = wrapped.status

		requestCounter.WithLabelValues(r.URL.Path, r.Method, http.StatusText(statusCode)).Add(1)
		requestDuration.WithLabelValues(r.URL.Path, r.Method).Observe(time.Since(start).Seconds())
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

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
		"service": "atlas-api",
	})
}

func defaultHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		_, _ = w.Write([]byte("Atlas API ready\n"))
		return
	}
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("not found\n"))
}

func init() {
	if value := os.Getenv("LOG_LEVEL"); value == "" {
		_ = os.Setenv("LOG_LEVEL", "info")
	}
}
