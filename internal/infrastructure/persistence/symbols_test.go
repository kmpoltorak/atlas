package persistence

import (
	"atlas/internal/domain/market"
	"testing"
)

func TestSymbolInfoByTickerOmitsTickersNeverUpserted(t *testing.T) {
	db := connectTestDB(t)

	info, err := db.SymbolInfoByTicker()
	if err != nil {
		t.Fatalf("SymbolInfoByTicker failed: %v", err)
	}
	if _, present := info["symbol-info-test-never-upserted"]; present {
		t.Fatal("expected a ticker that was never upserted to be absent")
	}
}

func TestUpsertSymbolInfoThenGetRoundTrips(t *testing.T) {
	db := connectTestDB(t)

	if err := db.UpsertSymbolInfo("symbol-info-test-spy", market.InstrumentInfo{
		Name: "State Street SPDR S&P 500 ETF Trust", Type: "ETF", Exchange: "NYSEArca",
	}); err != nil {
		t.Fatalf("UpsertSymbolInfo failed: %v", err)
	}

	info, err := db.SymbolInfoByTicker()
	if err != nil {
		t.Fatalf("SymbolInfoByTicker failed: %v", err)
	}
	got, ok := info["symbol-info-test-spy"]
	if !ok {
		t.Fatal("expected the upserted ticker to be present")
	}
	want := SymbolInfo{Name: "State Street SPDR S&P 500 ETF Trust", Exchange: "NYSEArca", AssetClass: "ETF"}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestUpsertSymbolQuoteThenGetRoundTrips(t *testing.T) {
	db := connectTestDB(t)

	if err := db.UpsertSymbolQuote("symbol-quote-test-orcl", market.Quote{Price: 146.605, PreviousClose: 151.05}); err != nil {
		t.Fatalf("UpsertSymbolQuote failed: %v", err)
	}

	info, err := db.SymbolInfoByTicker()
	if err != nil {
		t.Fatalf("SymbolInfoByTicker failed: %v", err)
	}
	got, ok := info["symbol-quote-test-orcl"]
	if !ok {
		t.Fatal("expected the quoted ticker to be present even with no name/type recorded")
	}
	if got.LastPrice != 146.605 || got.PreviousClose != 151.05 {
		t.Fatalf("got LastPrice=%v PreviousClose=%v, want 146.605/151.05", got.LastPrice, got.PreviousClose)
	}
	if got.QuoteUpdatedAt.IsZero() {
		t.Fatal("expected QuoteUpdatedAt to be set")
	}
}

func TestUpsertSymbolQuoteSkipsNonPositivePrice(t *testing.T) {
	db := connectTestDB(t)

	if err := db.UpsertSymbolQuote("symbol-quote-test-good", market.Quote{Price: 100, PreviousClose: 99}); err != nil {
		t.Fatalf("UpsertSymbolQuote failed: %v", err)
	}
	// A zero price (e.g. a transient provider hiccup) must not blank out the last good reading.
	if err := db.UpsertSymbolQuote("symbol-quote-test-good", market.Quote{Price: 0}); err != nil {
		t.Fatalf("UpsertSymbolQuote failed: %v", err)
	}

	info, err := db.SymbolInfoByTicker()
	if err != nil {
		t.Fatalf("SymbolInfoByTicker failed: %v", err)
	}
	if got := info["symbol-quote-test-good"].LastPrice; got != 100 {
		t.Fatalf("expected the last good price (100) to survive a zero-price update, got %v", got)
	}
}

func TestUpsertSymbolInfoOverwritesPreviousValues(t *testing.T) {
	db := connectTestDB(t)

	if err := db.UpsertSymbolInfo("symbol-info-test-overwrite", market.InstrumentInfo{Name: "Old Name", Type: "EQUITY"}); err != nil {
		t.Fatalf("UpsertSymbolInfo failed: %v", err)
	}
	if err := db.UpsertSymbolInfo("symbol-info-test-overwrite", market.InstrumentInfo{Name: "New Name", Type: "ETF"}); err != nil {
		t.Fatalf("UpsertSymbolInfo failed: %v", err)
	}

	info, err := db.SymbolInfoByTicker()
	if err != nil {
		t.Fatalf("SymbolInfoByTicker failed: %v", err)
	}
	got := info["symbol-info-test-overwrite"]
	if got.Name != "New Name" || got.AssetClass != "ETF" {
		t.Fatalf("expected the second upsert to win, got %+v", got)
	}
}
