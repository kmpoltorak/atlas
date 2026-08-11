package market

import "time"

type Bar struct {
	Symbol    string    `json:"symbol"`
	Open      float64   `json:"open"`
	High      float64   `json:"high"`
	Low       float64   `json:"low"`
	Close     float64   `json:"close"`
	Volume    float64   `json:"volume"`
	Timestamp time.Time `json:"timestamp"`
}

// InstrumentInfo describes what a symbol actually is — its display name and instrument type
// (e.g. "EQUITY", "ETF", "FUTURE", "CRYPTOCURRENCY", "INDEX", as reported by the provider) —
// so a human can recognize an unfamiliar ticker without looking it up elsewhere. Unlike Bar,
// this is effectively static, so callers fetch it once (see watchlist add/refresh) rather than
// treating it as time-series data refreshed on every collection.
type InstrumentInfo struct {
	Symbol   string
	Name     string
	Type     string
	Exchange string
}

type MarketProvider interface {
	FetchBars(symbol string, window int) ([]Bar, error)
	// FetchInstrumentInfo returns descriptive metadata for symbol, for a human reading the
	// watchlist — not consumed by the decision engine.
	FetchInstrumentInfo(symbol string) (InstrumentInfo, error)
}
