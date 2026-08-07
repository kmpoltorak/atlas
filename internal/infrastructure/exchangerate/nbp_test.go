package exchangerate

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"atlas/internal/domain/currency"
)

func TestFetchRatesParsesNBPResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{
			"table": "A", "no": "152/A/NBP/2026", "effectiveDate": "2026-08-07",
			"rates": [
				{"currency": "dolar amerykański", "code": "USD", "mid": 3.7324},
				{"currency": "euro", "code": "EUR", "mid": 4.2515},
				{"currency": "bat (Tajlandia)", "code": "THB", "mid": 0.1129}
			]
		}]`))
	}))
	defer server.Close()

	provider := NewNBPProvider()
	provider.BaseURL = server.URL
	provider.Client = server.Client()

	rates, err := provider.FetchRates()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rates[currency.USD] != 3.7324 {
		t.Fatalf("expected USD rate 3.7324, got %v", rates[currency.USD])
	}
	if rates[currency.EUR] != 4.2515 {
		t.Fatalf("expected EUR rate 4.2515, got %v", rates[currency.EUR])
	}
	if _, ok := rates[currency.Code("THB")]; ok {
		t.Fatalf("expected only EUR/USD to be extracted, THB is not a currency Atlas supports")
	}
}

func TestFetchRatesErrorsWhenACurrencyIsMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"table":"A","rates":[{"currency":"euro","code":"EUR","mid":4.2515}]}]`))
	}))
	defer server.Close()

	provider := NewNBPProvider()
	provider.BaseURL = server.URL
	provider.Client = server.Client()

	if _, err := provider.FetchRates(); err == nil {
		t.Fatal("expected an error when USD is missing from the response")
	}
}

func TestFetchRatesErrorsOnEmptyTableList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	provider := NewNBPProvider()
	provider.BaseURL = server.URL
	provider.Client = server.Client()

	if _, err := provider.FetchRates(); err == nil {
		t.Fatal("expected an error for an empty table list")
	}
}

func TestFetchRatesSendsUserAgent(t *testing.T) {
	var gotUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"table":"A","rates":[{"currency":"euro","code":"EUR","mid":4.2515},{"currency":"dolar amerykański","code":"USD","mid":3.7324}]}]`))
	}))
	defer server.Close()

	provider := NewNBPProvider()
	provider.BaseURL = server.URL
	provider.Client = server.Client()

	if _, err := provider.FetchRates(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotUserAgent == "" {
		t.Fatal("expected a non-empty User-Agent header; NBP's endpoint returns 403 for Go's default one")
	}
}

func TestFetchRatesErrorsOnNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	provider := NewNBPProvider()
	provider.BaseURL = server.URL
	provider.Client = server.Client()

	if _, err := provider.FetchRates(); err == nil {
		t.Fatal("expected an error for a non-200 status")
	}
}

// TestFetchRatesAgainstRealNBPEndpoint verifies live against the real NBP API rather than only
// a mocked response — skipped unless explicitly requested (ATLAS_LIVE_NBP_TEST=1), the same
// opt-in convention used for other real-network integration checks in this repo, so `go test`
// stays hermetic by default.
func TestFetchRatesAgainstRealNBPEndpoint(t *testing.T) {
	if os.Getenv("ATLAS_LIVE_NBP_TEST") != "1" {
		t.Skip("ATLAS_LIVE_NBP_TEST not set; skipping live NBP integration test")
	}
	provider := NewNBPProvider()
	rates, err := provider.FetchRates()
	if err != nil {
		t.Fatalf("unexpected error calling the real NBP API: %v", err)
	}
	if rates[currency.USD] <= 0 || rates[currency.EUR] <= 0 {
		t.Fatalf("expected positive real EUR/USD rates, got %+v", rates)
	}
}
