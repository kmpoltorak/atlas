package app

import (
	"testing"

	"atlas/internal/domain/watchlist"
	"atlas/internal/infrastructure/persistence"
)

func TestCountActionableSkipsHoldAndUntrackedSymbols(t *testing.T) {
	streaks := map[string]persistence.ActionStreak{
		"AAPL": {Symbol: "AAPL", Action: "BUY"},
		"MSFT": {Symbol: "MSFT", Action: "BUY"},
		"ORCL": {Symbol: "ORCL", Action: "HOLD"},
		"TSLA": {Symbol: "TSLA", Action: "SELL"},
		"GDX":  {Symbol: "GDX", Action: "BUY"}, // recorded, but no longer on the watchlist
	}
	items := []watchlist.Item{{Symbol: "AAPL"}, {Symbol: "MSFT"}, {Symbol: "ORCL"}, {Symbol: "TSLA"}}

	counts := countActionable(streaks, items)
	if counts["BUY"] != 2 {
		t.Fatalf("expected 2 BUY (GDX is off the watchlist), got %d", counts["BUY"])
	}
	if counts["SELL"] != 1 {
		t.Fatalf("expected 1 SELL, got %d", counts["SELL"])
	}
	if _, present := counts["HOLD"]; present {
		t.Fatal("HOLD is not an alert and must not be counted")
	}
}

func TestCountActionableReturnsNilWhenNothingIsActionable(t *testing.T) {
	streaks := map[string]persistence.ActionStreak{"AAPL": {Symbol: "AAPL", Action: "HOLD"}}
	if counts := countActionable(streaks, []watchlist.Item{{Symbol: "AAPL"}}); counts != nil {
		t.Fatalf("expected nil so the card renders empty, got %v", counts)
	}
}
