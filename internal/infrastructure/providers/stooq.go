package providers

import (
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"atlas/internal/domain/market"
)

// StooqProvider fetches historical OHLCV bars from Stooq's free, keyless CSV endpoint.
//
// Yahoo Finance has no official, supported API for market data (see PROJECT_CONTEXT.md);
// its unofficial chart endpoint is prone to rate limiting. Stooq is the documented free
// fallback and requires no API key or OAuth flow.
type StooqProvider struct {
	BaseURL string
	Client  *http.Client
}

func NewStooqProvider() *StooqProvider {
	return &StooqProvider{
		BaseURL: "https://stooq.com/q/d/l",
		Client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *StooqProvider) FetchBars(symbol string, window int) ([]market.Bar, error) {
	if symbol == "" {
		return nil, fmt.Errorf("symbol is required")
	}
	if window <= 0 {
		window = 30 // mirrors config.Load's COLLECTOR_WINDOW_DAYS default; guards direct calls (e.g. tests) that skip it
	}

	to := time.Now().UTC()
	from := to.AddDate(0, 0, -window)
	url := fmt.Sprintf("%s/?s=%s&d1=%s&d2=%s&i=d", p.BaseURL, stooqSymbol(symbol), from.Format("20060102"), to.Format("20060102"))

	resp, err := p.Client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected provider status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	bars, err := parseStooqCSV(symbol, body)
	if err != nil {
		return nil, err
	}
	if len(bars) == 0 {
		return nil, fmt.Errorf("no result returned for symbol %s", symbol)
	}
	return bars, nil
}

// stooqSymbol maps a plain ticker (e.g. "AAPL") to Stooq's exchange-suffixed form
// (e.g. "aapl.us"). Symbols that already carry an exchange suffix pass through unchanged.
func stooqSymbol(symbol string) string {
	lower := strings.ToLower(symbol)
	if strings.Contains(lower, ".") {
		return lower
	}
	return lower + ".us"
}

// parseStooqCSV parses Stooq's "Date,Open,High,Low,Close,Volume" CSV response. An unknown
// symbol yields a body that isn't a valid Date-headed CSV, which is treated as no data.
// Some networks receive an HTML bot-verification challenge instead of CSV; that is reported
// clearly rather than as an opaque CSV parse error, and Atlas does not attempt to solve it.
func parseStooqCSV(symbol string, body []byte) ([]market.Bar, error) {
	if trimmed := strings.TrimSpace(string(body)); strings.HasPrefix(trimmed, "<!DOCTYPE") || strings.HasPrefix(trimmed, "<html") {
		return nil, fmt.Errorf("stooq returned a bot-verification challenge instead of data for symbol %s; this endpoint may not be reachable from this network", symbol)
	}

	reader := csv.NewReader(strings.NewReader(string(body)))
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("invalid csv response for symbol %s: %w", symbol, err)
	}
	if len(records) == 0 || len(records[0]) == 0 || !strings.EqualFold(records[0][0], "Date") {
		return nil, fmt.Errorf("no result returned for symbol %s", symbol)
	}

	bars := make([]market.Bar, 0, len(records)-1)
	for _, row := range records[1:] {
		if len(row) < 6 {
			continue
		}
		timestamp, err := time.Parse("2006-01-02", row[0])
		if err != nil {
			continue
		}
		bars = append(bars, market.Bar{
			Symbol:    symbol,
			Open:      mustFloat(row[1]),
			High:      mustFloat(row[2]),
			Low:       mustFloat(row[3]),
			Close:     mustFloat(row[4]),
			Volume:    mustFloat(row[5]),
			Timestamp: timestamp,
		})
	}
	return bars, nil
}
