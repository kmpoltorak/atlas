package observability

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNewMetricsRegistersEveryCollector(t *testing.T) {
	m := NewMetrics()

	m.RequestCounter.WithLabelValues("/health", "GET", "200").Inc()
	m.RequestDuration.WithLabelValues("/health", "GET").Observe(0.1)
	m.ProviderRequests.WithLabelValues("yahoo", "success").Inc()
	m.RecommendationsTotal.WithLabelValues("BUY").Inc()
	m.PredictionsTotal.WithLabelValues("v1").Inc()
	m.AlertsSent.WithLabelValues("SELL").Inc()
	m.AlertPollErrors.Inc()
	m.DependencyUp.WithLabelValues("database").Set(1)

	if got := testutil.ToFloat64(m.AlertPollErrors); got != 1 {
		t.Errorf("AlertPollErrors after one Inc() = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.RecommendationsTotal.WithLabelValues("BUY")); got != 1 {
		t.Errorf("RecommendationsTotal{action=BUY} after one Inc() = %v, want 1", got)
	}

	// Every metric above was incremented, so it must actually be registered on m.Registry (not
	// just constructed) for the scrape to see it — a metric built but left out of MustRegister
	// would silently never appear here despite the .Inc() calls above succeeding.
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, name := range []string{
		"atlas_api_requests_total",
		"atlas_request_duration_seconds",
		"atlas_provider_requests_total",
		"atlas_recommendations_total",
		"atlas_predictions_total",
		"atlas_alerts_sent_total",
		"atlas_alert_poll_errors_total",
		"atlas_dependency_up",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("scraped output missing metric %q", name)
		}
	}
}

func TestNewMetricsEachCallIsIndependent(t *testing.T) {
	// NewMetrics uses its own fresh prometheus.Registry per call — calling it twice must not
	// panic with "duplicate metrics collector registration attempted" the way registering the
	// same collector name twice on one shared/global registry would.
	NewMetrics()
	NewMetrics()
}
