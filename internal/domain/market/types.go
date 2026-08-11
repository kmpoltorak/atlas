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

// Quote is a symbol's current live price and the prior session's close — enough to show "how is
// this doing today", independent of Bar's closed-candle history (see providers.dropUnclosedBars'
// doc comment for why the decision engine deliberately never acts on an intraday, still-forming
// price). Purely informational: nothing in the decision engine reads this.
type Quote struct {
	Symbol        string
	Price         float64
	PreviousClose float64
	// Currency is the instrument's native trading currency (e.g. "USD" for a US stock, "PLN"
	// for a WSE listing), exactly as the provider reports it — an ISO 4217 code, but not
	// necessarily one of currency.Valid's three; callers that need to combine this with
	// PLN-denominated portfolio figures (Position.CurrentPrice) must convert first. May be
	// empty if the provider didn't report one.
	Currency string
}

type MarketProvider interface {
	FetchBars(symbol string, window int) ([]Bar, error)
	// FetchInstrumentInfo returns descriptive metadata for symbol, for a human reading the
	// watchlist — not consumed by the decision engine.
	FetchInstrumentInfo(symbol string) (InstrumentInfo, error)
	// FetchQuote returns symbol's current live price and prior close — see Quote.
	FetchQuote(symbol string) (Quote, error)
}
