package providers

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestFetchBarsParsesProviderResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"chart": {
				"result": [{
					"timestamp": [1704067200, 1704153600],
					"indicators": {
						"quote": [{
							"open": [100, 101],
							"high": [105, 106],
							"low": [99, 100],
							"close": [104, 105],
							"volume": [1000, 1100]
						}]
					}
				}]
			}
		}`))
	}))
	defer server.Close()

	provider := NewYahooFinanceProvider()
	provider.BaseURL = server.URL + "/v8/finance/chart"
	provider.Client = server.Client()

	bars, err := provider.FetchBars("AAPL", 2)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(bars) != 2 {
		t.Fatalf("expected two bars, got %d", len(bars))
	}
	if bars[0].Symbol != "AAPL" {
		t.Fatalf("expected symbol AAPL, got %q", bars[0].Symbol)
	}
	if bars[0].Timestamp != time.Unix(1704067200, 0) {
		t.Fatalf("unexpected first timestamp: %v", bars[0].Timestamp)
	}
}
