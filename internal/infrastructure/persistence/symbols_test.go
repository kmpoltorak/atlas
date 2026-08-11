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
