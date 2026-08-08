package observability

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	Registry        *prometheus.Registry
	RequestCounter  *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec

	// ProviderRequests counts market data provider calls, labeled by provider name and
	// outcome (success/error), satisfying PROJECT_CONTEXT.md's "provider metrics" requirement.
	ProviderRequests *prometheus.CounterVec
	// RecommendationsTotal counts recommendations issued by the decision engine, labeled by
	// action (BUY/SELL/HOLD/...), satisfying the "decision metrics" requirement.
	RecommendationsTotal *prometheus.CounterVec
	// PredictionsTotal counts ML predictions served, labeled by model version, satisfying the
	// "ML metrics" requirement.
	PredictionsTotal *prometheus.CounterVec
	// AlertsSent counts Discord alert notifications actually delivered, labeled by action.
	AlertsSent *prometheus.CounterVec
	// AlertPollErrors counts background alert-poll cycles (StartAlertPolling) that failed to
	// evaluate the watchlist at all — fed into prometheus/alerts.yml's AtlasAlertPollErrors
	// rule, routed to the infra-discord channel like every other systemic-health alert.
	AlertPollErrors prometheus.Counter
}

func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	counter := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "atlas_api_requests_total",
			Help: "Total number of HTTP requests handled by Atlas API.",
		},
		[]string{"path", "method", "status"},
	)
	duration := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "atlas_request_duration_seconds",
			Help:    "HTTP request duration in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"path", "method"},
	)
	providerRequests := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "atlas_provider_requests_total",
			Help: "Total number of market data provider requests, by provider and outcome.",
		},
		[]string{"provider", "outcome"},
	)
	recommendationsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "atlas_recommendations_total",
			Help: "Total number of recommendations issued by the decision engine, by action.",
		},
		[]string{"action"},
	)
	predictionsTotal := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "atlas_predictions_total",
			Help: "Total number of ML predictions served, by model version.",
		},
		[]string{"model_version"},
	)
	alertsSent := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "atlas_alerts_sent_total",
			Help: "Total number of Discord alert notifications delivered, by action.",
		},
		[]string{"action"},
	)
	alertPollErrors := prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "atlas_alert_poll_errors_total",
			Help: "Total number of background alert-poll cycles that failed to evaluate the watchlist.",
		},
	)
	registry.MustRegister(counter, duration, providerRequests, recommendationsTotal, predictionsTotal, alertsSent, alertPollErrors)

	return &Metrics{
		Registry:        registry,
		RequestCounter:  counter,
		RequestDuration: duration,

		ProviderRequests:     providerRequests,
		RecommendationsTotal: recommendationsTotal,
		PredictionsTotal:     predictionsTotal,
		AlertsSent:           alertsSent,
		AlertPollErrors:      alertPollErrors,
	}
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}
