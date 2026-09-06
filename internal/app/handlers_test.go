package app

import (
	"testing"
	"time"

	"atlas/internal/domain/decision"
	"atlas/internal/domain/portfolio"
	"atlas/internal/domain/watchlist"
	"atlas/internal/infrastructure/persistence"
)

// The valuation itself now happens once in the SQL layer (see persistence.positionRow.
// toPosition); what is left here is turning it into the per-symbol "as of" the templates show.
func TestLivePricedPositionsReportsTheQuoteTimestamp(t *testing.T) {
	quotedAt := time.Date(2026, 8, 11, 14, 46, 59, 0, time.UTC)
	positions := []portfolio.Position{{
		Symbol: "ETFBDIVPL.WA", Quantity: 10, AverageCost: 302.55, CurrentPrice: 301.6,
		PriceSource: portfolio.PriceFromQuote, PricedAt: quotedAt,
	}}
	asOf := map[string]string{"ETFBDIVPL.WA": "2026-08-11T12:18:33Z"}

	priced, live := (&Application{}).livePricedPositions(positions, asOf)

	if priced[0].CurrentPrice != 301.6 || priced[0].AverageCost != 302.55 || priced[0].Quantity != 10 {
		t.Fatalf("the valuation must pass through untouched, got %+v", priced[0])
	}
	if live["ETFBDIVPL.WA"] != quotedAt.Format(time.RFC3339) {
		t.Fatalf("expected asOf to be the quote timestamp, got %q", live["ETFBDIVPL.WA"])
	}
}

// A position priced from its last trade keeps the transaction-time "as of", so the page says
// when the number is from instead of implying it is a current market price.
func TestLivePricedPositionsKeepsTransactionTimeWithoutAQuote(t *testing.T) {
	positions := []portfolio.Position{{Symbol: "AAPL", CurrentPrice: 200, PriceSource: portfolio.PriceFromTrade}}
	asOf := map[string]string{"AAPL": "2026-08-01T00:00:00Z"}

	priced, live := (&Application{}).livePricedPositions(positions, asOf)

	if priced[0].CurrentPrice != 200 {
		t.Fatalf("expected the trade price to survive, got %v", priced[0].CurrentPrice)
	}
	if live["AAPL"] != "2026-08-01T00:00:00Z" {
		t.Fatalf("expected asOf to keep the transaction timestamp, got %q", live["AAPL"])
	}
}

func TestFilterCurrentAlertsSkipsHoldAndUntrackedSymbols(t *testing.T) {
	since := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	recommendations := map[string]persistence.LatestRecommendation{
		"AAPL": {Recommendation: decision.Recommendation{Symbol: "AAPL", Action: "BUY"}, CreatedAt: since},
		"ORCL": {Recommendation: decision.Recommendation{Symbol: "ORCL", Action: "HOLD"}, CreatedAt: since},
		"GDX":  {Recommendation: decision.Recommendation{Symbol: "GDX", Action: "SELL"}, CreatedAt: since}, // recorded, but no longer on the watchlist
	}
	items := []watchlist.Item{{Symbol: "AAPL"}, {Symbol: "ORCL"}, {Symbol: "MSFT"}}

	result := filterCurrentAlerts(recommendations, items)
	if len(result) != 1 || result[0].Recommendation.Symbol != "AAPL" {
		t.Fatalf("expected only AAPL (ORCL is HOLD, GDX is off the watchlist, MSFT has no recorded recommendation), got %+v", result)
	}
	if result[0].TriggeredAt != since.Format(time.RFC3339) {
		t.Errorf("TriggeredAt = %q, want %q", result[0].TriggeredAt, since.Format(time.RFC3339))
	}
}

func TestSymbolsMissingInfoSkipsSymbolsAlreadyRecorded(t *testing.T) {
	items := []watchlist.Item{{Symbol: "AAPL"}, {Symbol: "MSFT"}, {Symbol: "SPY"}}
	existing := map[string]persistence.SymbolInfo{
		"AAPL": {Name: "Apple Inc.", AssetClass: "EQUITY"},
	}

	missing := symbolsMissingInfo(items, existing)
	if len(missing) != 2 || missing[0] != "MSFT" || missing[1] != "SPY" {
		t.Fatalf("expected [MSFT SPY], got %v", missing)
	}
}

func TestSymbolsMissingInfoReturnsEmptyWhenEverythingIsRecorded(t *testing.T) {
	items := []watchlist.Item{{Symbol: "AAPL"}}
	existing := map[string]persistence.SymbolInfo{"AAPL": {Name: "Apple Inc.", AssetClass: "EQUITY"}}

	if missing := symbolsMissingInfo(items, existing); len(missing) != 0 {
		t.Fatalf("expected no missing symbols, got %v", missing)
	}
}

func TestFilterCurrentAlertsReturnsEmptyWhenNothingIsActionable(t *testing.T) {
	recommendations := map[string]persistence.LatestRecommendation{
		"AAPL": {Recommendation: decision.Recommendation{Symbol: "AAPL", Action: "HOLD"}},
	}
	result := filterCurrentAlerts(recommendations, []watchlist.Item{{Symbol: "AAPL"}})
	if len(result) != 0 {
		t.Fatalf("expected no alerts, got %+v", result)
	}
}

func TestDecodeWatchlistImportAcceptsBothShapes(t *testing.T) {
	// The export shape, which must round-trip exactly.
	items, err := decodeWatchlistImport([]byte(`{"items":[{"symbol":"AAPL","added_at":"2026-01-01T00:00:00Z"}]}`))
	if err != nil {
		t.Fatalf("export shape: %v", err)
	}
	if len(items) != 1 || items[0].Symbol != "AAPL" || items[0].AddedAt != "2026-01-01T00:00:00Z" {
		t.Errorf("export shape decoded to %+v", items)
	}

	// A hand-written list of tickers, which is the other realistic way this file gets made.
	items, err = decodeWatchlistImport([]byte(`["AAPL","MSFT"]`))
	if err != nil {
		t.Fatalf("bare list: %v", err)
	}
	if len(items) != 2 || items[1].Symbol != "MSFT" {
		t.Errorf("bare list decoded to %+v", items)
	}
	if items[0].AddedAt != "" {
		t.Errorf("a bare list carries no dates; Import stamps them, got %q", items[0].AddedAt)
	}
}

func TestDecodeWatchlistImportRejectsJunk(t *testing.T) {
	for name, body := range map[string]string{
		"empty":        ``,
		"whitespace":   "  \n ",
		"not json":     `hello`,
		"wrong object": `{"symbols":["AAPL"]}`,
		"truncated":    `{"items":[`,
		"scalar":       `42`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeWatchlistImport([]byte(body)); err == nil {
				t.Errorf("expected %q to be rejected", body)
			}
		})
	}
}
