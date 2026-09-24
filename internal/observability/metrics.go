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

	// ProviderRequests counts market data provider calls, labeled by provider name, outcome
	// (success/error), and kind ("bars": historical OHLCV via Collector.Collect, "quote": a live
	// price via Provider.FetchQuote directly — see alerts_loop.go's refreshWatchlistQuotes) so a
	// provider outage shows which call shape is actually failing.
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
	// DependencyUp is 1/0 per dependency (labeled "dependency": database/redis/trainer/
	// prometheus/alertmanager/grafana), kept fresh by StartDependencyStatusPolling — the same
	// checks /health itself runs (see Application.dependencyStatuses), just as a gauge Grafana
	// can graph over time instead of a point-in-time JSON response. atlas-api's own liveness is
	// already covered by Prometheus' built-in `up{job="atlas-api"}` — this covers everything
	// atlas-api can't self-report by definition (if atlas-api is down, it can't push its own
	// gauge either), which is why atlas-api isn't one of the dependency labels here.
	DependencyUp *prometheus.GaugeVec
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
			Help: "Total number of market data provider requests, by provider, outcome, and kind (bars/quote).",
		},
		[]string{"provider", "outcome", "kind"},
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
	dependencyUp := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "atlas_dependency_up",
			Help: "Whether a dependency atlas-api talked to on its last check was reachable (1) or not (0).",
		},
		[]string{"dependency"},
	)
	registry.MustRegister(counter, duration, providerRequests, recommendationsTotal, predictionsTotal, alertsSent, alertPollErrors, dependencyUp)

	return &Metrics{
		Registry:        registry,
		RequestCounter:  counter,
		RequestDuration: duration,

		ProviderRequests:     providerRequests,
		RecommendationsTotal: recommendationsTotal,
		PredictionsTotal:     predictionsTotal,
		AlertsSent:           alertsSent,
		AlertPollErrors:      alertPollErrors,
		DependencyUp:         dependencyUp,
	}
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{})
}
