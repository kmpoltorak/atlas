package providers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStooqFetchBarsParsesCSVResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		_, _ = w.Write([]byte("Date,Open,High,Low,Close,Volume\n" +
			"2024-01-02,100,105,99,104,1000\n" +
			"2024-01-03,104,106,103,105,1100\n"))
	}))
	defer server.Close()

	provider := NewStooqProvider()
	provider.BaseURL = server.URL
	provider.Client = server.Client()

	bars, err := provider.FetchBars("AAPL", 5)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(bars) != 2 {
		t.Fatalf("expected two bars, got %d", len(bars))
	}
	if bars[0].Symbol != "AAPL" {
		t.Fatalf("expected symbol AAPL, got %q", bars[0].Symbol)
	}
	if bars[1].Close != 105 {
		t.Fatalf("expected second close of 105, got %v", bars[1].Close)
	}
	wantTimestamp, _ := time.Parse("2006-01-02", "2024-01-02")
	if !bars[0].Timestamp.Equal(wantTimestamp) {
		t.Fatalf("unexpected first timestamp: %v", bars[0].Timestamp)
	}
}

func TestStooqFetchBarsRejectsUnknownSymbol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("Symbol not found\n"))
	}))
	defer server.Close()

	provider := NewStooqProvider()
	provider.BaseURL = server.URL
	provider.Client = server.Client()

	if _, err := provider.FetchBars("NOTASYMBOL", 5); err == nil {
		t.Fatal("expected an error for an unknown symbol response")
	}
}

func TestStooqFetchBarsReportsBotChallengeClearly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!DOCTYPE html><html><body>verify your browser</body></html>`))
	}))
	defer server.Close()

	provider := NewStooqProvider()
	provider.BaseURL = server.URL
	provider.Client = server.Client()

	_, err := provider.FetchBars("AAPL", 5)
	if err == nil {
		t.Fatal("expected an error for an HTML bot-challenge response")
	}
	if !strings.Contains(err.Error(), "bot-verification challenge") {
		t.Fatalf("expected a clear bot-challenge error message, got: %v", err)
	}
}

func TestStooqFetchBarsRejectsMissingSymbol(t *testing.T) {
	provider := NewStooqProvider()
	if _, err := provider.FetchBars("", 5); err == nil {
		t.Fatal("expected an error for a missing symbol")
	}
}

func TestStooqSymbolAddsUSExchangeSuffix(t *testing.T) {
	if got := stooqSymbol("AAPL"); got != "aapl.us" {
		t.Fatalf("expected aapl.us, got %q", got)
	}
	if got := stooqSymbol("aapl.us"); got != "aapl.us" {
		t.Fatalf("expected passthrough for already-suffixed symbol, got %q", got)
	}
}
