package providers

import (
	"fmt"
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

func TestFetchInstrumentInfoParsesMeta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"chart": {
				"result": [{
					"meta": {
						"instrumentType": "ETF",
						"shortName": "SPDR S&P 500",
						"longName": "State Street SPDR S&P 500 ETF Trust",
						"fullExchangeName": "NYSEArca"
					}
				}]
			}
		}`))
	}))
	defer server.Close()

	provider := NewYahooFinanceProvider()
	provider.BaseURL = server.URL + "/v8/finance/chart"
	provider.Client = server.Client()

	info, err := provider.FetchInstrumentInfo("SPY")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	want := struct{ Name, Type, Exchange string }{"State Street SPDR S&P 500 ETF Trust", "ETF", "NYSEArca"}
	if info.Name != want.Name || info.Type != want.Type || info.Exchange != want.Exchange {
		t.Fatalf("got %+v, want %+v", info, want)
	}
}

// Futures contracts (e.g. "CL=F") are the observed real-world case: Yahoo leaves longName empty
// for them but still sets shortName, so FetchInstrumentInfo must fall back rather than return "".
func TestFetchInstrumentInfoFallsBackToShortNameWhenLongNameIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"chart": {
				"result": [{
					"meta": {
						"instrumentType": "FUTURE",
						"shortName": "Crude Oil Sep 26",
						"longName": "",
						"fullExchangeName": "NY Mercantile"
					}
				}]
			}
		}`))
	}))
	defer server.Close()

	provider := NewYahooFinanceProvider()
	provider.BaseURL = server.URL + "/v8/finance/chart"
	provider.Client = server.Client()

	info, err := provider.FetchInstrumentInfo("CL=F")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if info.Name != "Crude Oil Sep 26" {
		t.Fatalf("expected shortName fallback, got %q", info.Name)
	}
}

func TestFetchQuoteParsesMeta(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"chart": {
				"result": [{
					"meta": {
						"regularMarketPrice": 146.605,
						"chartPreviousClose": 151.05,
						"currency": "USD"
					}
				}]
			}
		}`))
	}))
	defer server.Close()

	provider := NewYahooFinanceProvider()
	provider.BaseURL = server.URL + "/v8/finance/chart"
	provider.Client = server.Client()

	quote, err := provider.FetchQuote("ORCL")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if quote.Symbol != "ORCL" || quote.Price != 146.605 || quote.PreviousClose != 151.05 || quote.Currency != "USD" {
		t.Fatalf("unexpected quote: %+v", quote)
	}
}

// Yahoo appends the live quote to an intraday series as an extra data point timestamped at the
// moment of the request, with OHLC all equal to the current price and volume 0. Every consumer
// downstream treats it as a finished candle and gets a zero-momentum, zero-volume reading out of
// it, so FetchBars must not return it — see dropUnclosedBars.
func TestFetchBarsDropsTheUnclosedTrailingBar(t *testing.T) {
	const closedBarStart int64 = 1704067200 // candle covering [closedBarStart, +1h)
	liveQuoteAt := closedBarStart + 4500

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{
			"chart": {
				"result": [{
					"timestamp": [%d, %d],
					"indicators": {
						"quote": [{
							"open": [100, 107],
							"high": [105, 107],
							"low": [99, 107],
							"close": [104, 107],
							"volume": [1000, 0]
						}]
					}
				}]
			}
		}`, closedBarStart, liveQuoteAt)))
	}))
	defer server.Close()

	provider := NewYahooFinanceProvider()
	provider.BaseURL = server.URL + "/v8/finance/chart"
	provider.Client = server.Client()
	// One second past the live quote: the first candle has closed, the second has not.
	provider.Now = func() time.Time { return time.Unix(liveQuoteAt+1, 0) }

	bars, err := provider.FetchBars("AAPL", 2)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(bars) != 1 {
		t.Fatalf("expected the unclosed bar to be dropped, got %d bars", len(bars))
	}
	if bars[0].Timestamp != time.Unix(closedBarStart, 0) {
		t.Fatalf("expected the closed bar to survive, got %v", bars[0].Timestamp)
	}
}

// The mirror of the case above: once a candle's interval has elapsed it is real history, even
// though it is the newest bar in the series. Dropping it would throw away the most recent
// closed candle on every poll.
func TestFetchBarsKeepsTheNewestClosedBar(t *testing.T) {
	const barStart = 1704067200

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{
			"chart": {"result": [{
				"timestamp": [%d],
				"indicators": {"quote": [{"open":[100],"high":[105],"low":[99],"close":[104],"volume":[1000]}]}
			}]}
		}`, barStart)))
	}))
	defer server.Close()

	provider := NewYahooFinanceProvider()
	provider.BaseURL = server.URL + "/v8/finance/chart"
	provider.Client = server.Client()
	// Exactly at the close boundary — the candle is finished, not forming.
	provider.Now = func() time.Time { return time.Unix(barStart, 0).Add(time.Hour) }

	bars, err := provider.FetchBars("AAPL", 2)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(bars) != 1 {
		t.Fatalf("expected the closed bar to be kept, got %d bars", len(bars))
	}
}

// A flat, zero-volume hour is real history for an illiquid instrument, so the filter must key on
// the clock rather than on the row looking degenerate.
func TestFetchBarsKeepsAFlatClosedBar(t *testing.T) {
	const barStart = 1704067200

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{
			"chart": {"result": [{
				"timestamp": [%d],
				"indicators": {"quote": [{"open":[100],"high":[100],"low":[100],"close":[100],"volume":[0]}]}
			}]}
		}`, barStart)))
	}))
	defer server.Close()

	provider := NewYahooFinanceProvider()
	provider.BaseURL = server.URL + "/v8/finance/chart"
	provider.Client = server.Client()
	provider.Now = func() time.Time { return time.Unix(barStart, 0).Add(2 * time.Hour) }

	bars, err := provider.FetchBars("AAPL", 2)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(bars) != 1 {
		t.Fatalf("expected a flat but closed bar to be kept, got %d bars", len(bars))
	}
}

func TestFetchBarsRequestsHourlyInterval(t *testing.T) {
	var gotInterval string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotInterval = r.URL.Query().Get("interval")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"chart":{"result":[{"timestamp":[],"indicators":{"quote":[{"open":[],"high":[],"low":[],"close":[],"volume":[]}]}}]}}`))
	}))
	defer server.Close()

	provider := NewYahooFinanceProvider()
	provider.BaseURL = server.URL + "/v8/finance/chart"
	provider.Client = server.Client()

	if _, err := provider.FetchBars("AAPL", 5); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Daily bars are too coarse for trading — Atlas requests hourly candles.
	if gotInterval != "1h" {
		t.Fatalf("expected interval=1h, got %q", gotInterval)
	}
}

func TestFetchBarsSendsBrowserUserAgent(t *testing.T) {
	var gotUserAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"chart":{"result":[{"timestamp":[],"indicators":{"quote":[{"open":[],"high":[],"low":[],"close":[],"volume":[]}]}}]}}`))
	}))
	defer server.Close()

	provider := NewYahooFinanceProvider()
	provider.BaseURL = server.URL + "/v8/finance/chart"
	provider.Client = server.Client()

	if _, err := provider.FetchBars("AAPL", 5); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if gotUserAgent == "" {
		t.Fatal("expected a non-empty User-Agent header; Yahoo's endpoint returns 429 without one")
	}
}

// Yahoo emits a timestamp for every slot in the interval grid, including exchange holidays, and
// fills the OHLC arrays with null there. null unmarshals to 0, so without a guard those arrive
// as bars priced at zero — which the backtester reads as a total wipeout and the indicators read
// as a crash to zero and back. See hasUsablePrices.
func TestFetchBarsDropsHolidayBarsWithNullPrices(t *testing.T) {
	const barStart int64 = 1704067200

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{
			"chart": {"result": [{
				"timestamp": [%d, %d, %d],
				"indicators": {"quote": [{
					"open":   [100, null, 106],
					"high":   [105, null, 108],
					"low":    [99,  null, 105],
					"close":  [104, null, 107],
					"volume": [1000, null, 1200]
				}]}
			}]}
		}`, barStart, barStart+3600, barStart+7200)))
	}))
	defer server.Close()

	provider := NewYahooFinanceProvider()
	provider.BaseURL = server.URL + "/v8/finance/chart"
	provider.Client = server.Client()
	provider.Now = func() time.Time { return time.Unix(barStart+7200, 0).Add(2 * time.Hour) }

	bars, err := provider.FetchBars("PKN.WA", 2)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(bars) != 2 {
		t.Fatalf("expected the null-priced holiday bar to be dropped, got %d bars", len(bars))
	}
	for _, bar := range bars {
		if bar.Close <= 0 {
			t.Fatalf("a zero-priced bar survived: %+v", bar)
		}
	}
	// The bars either side of the hole must be untouched — this drops holidays, not history.
	if bars[0].Close != 104 || bars[1].Close != 107 {
		t.Fatalf("expected the real bars to pass through unchanged, got %v and %v", bars[0].Close, bars[1].Close)
	}
}
