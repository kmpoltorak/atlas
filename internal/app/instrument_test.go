package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"atlas/internal/observability"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// The AtlasHighErrorRate alert matches status=~"5..", so the label must be the numeric code.
func TestInstrumentLabelsRequestsWithNumericStatus(t *testing.T) {
	a := &Application{Metrics: observability.NewMetrics()}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	a.instrument(mux).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))

	if got := testutil.ToFloat64(a.Metrics.RequestCounter.WithLabelValues("/health", "GET", "503")); got != 1 {
		t.Errorf(`requests{path="/health",status="503"} = %v, want 1`, got)
	}
}
