package persistence

import (
	"testing"

	"atlas/internal/domain/portfolio"
)

func TestSavePositionsAndGetPositionsRoundTrip(t *testing.T) {
	db := connectTestDB(t)

	positions := []portfolio.Position{
		{Symbol: "TESTPORT1", Quantity: 5, AverageCost: 100, CurrentPrice: 110},
		{Symbol: "TESTPORT2", Quantity: 2, AverageCost: 200, CurrentPrice: 190},
	}
	if err := db.SavePositions(positions); err != nil {
		t.Fatalf("SavePositions failed: %v", err)
	}

	stored, err := db.GetPositions()
	if err != nil {
		t.Fatalf("GetPositions failed: %v", err)
	}

	found := map[string]portfolio.Position{}
	for _, p := range stored {
		found[p.Symbol] = p
	}
	if found["TESTPORT1"].Quantity != 5 || found["TESTPORT1"].CurrentPrice != 110 {
		t.Fatalf("unexpected TESTPORT1: %+v", found["TESTPORT1"])
	}
	if found["TESTPORT2"].Quantity != 2 || found["TESTPORT2"].CurrentPrice != 190 {
		t.Fatalf("unexpected TESTPORT2: %+v", found["TESTPORT2"])
	}
}

func TestSavePositionsIsIdempotent(t *testing.T) {
	db := connectTestDB(t)

	if err := db.SavePositions([]portfolio.Position{
		{Symbol: "TESTIDEMPPOS", Quantity: 1, AverageCost: 50, CurrentPrice: 50},
	}); err != nil {
		t.Fatalf("first SavePositions failed: %v", err)
	}
	// Re-saving the same symbol must overwrite in place, not duplicate the row — matches
	// how the use case recomputes and re-saves the whole position slice on every trade.
	if err := db.SavePositions([]portfolio.Position{
		{Symbol: "TESTIDEMPPOS", Quantity: 3, AverageCost: 55, CurrentPrice: 60},
	}); err != nil {
		t.Fatalf("second SavePositions failed: %v", err)
	}

	stored, err := db.GetPositions()
	if err != nil {
		t.Fatalf("GetPositions failed: %v", err)
	}
	count := 0
	var match portfolio.Position
	for _, p := range stored {
		if p.Symbol == "TESTIDEMPPOS" {
			count++
			match = p
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one TESTIDEMPPOS row, got %d", count)
	}
	if match.Quantity != 3 || match.CurrentPrice != 60 {
		t.Fatalf("expected the row to be overwritten, got %+v", match)
	}
}

func TestDeletePositionRemovesTheRow(t *testing.T) {
	db := connectTestDB(t)

	if err := db.SavePositions([]portfolio.Position{
		{Symbol: "TESTDELPOS", Quantity: 1, AverageCost: 50, CurrentPrice: 55},
	}); err != nil {
		t.Fatalf("SavePositions failed: %v", err)
	}

	if err := db.DeletePosition("TESTDELPOS"); err != nil {
		t.Fatalf("DeletePosition failed: %v", err)
	}

	stored, err := db.GetPositions()
	if err != nil {
		t.Fatalf("GetPositions failed: %v", err)
	}
	for _, p := range stored {
		if p.Symbol == "TESTDELPOS" {
			t.Fatalf("expected TESTDELPOS to be gone, still found: %+v", p)
		}
	}
}

func TestDeletePositionForUnknownSymbolIsANoop(t *testing.T) {
	db := connectTestDB(t)
	if err := db.DeletePosition("TESTDELPOSNEVEREXISTED"); err != nil {
		t.Fatalf("expected no error deleting a position that was never saved, got %v", err)
	}
}

func TestSaveTransactionInsertsRecord(t *testing.T) {
	db := connectTestDB(t)

	tx := portfolio.Transaction{
		Symbol: "TESTTX", Type: "BUY", Quantity: 4, Price: 25, Timestamp: "2026-08-06T00:00:00Z",
	}
	if err := db.SaveTransaction(tx); err != nil {
		t.Fatalf("SaveTransaction failed: %v", err)
	}
	// SaveTransaction has no read counterpart on the Repository interface (the use case
	// keeps its own in-memory transaction log) — this only verifies the insert itself
	// doesn't error, including the unparseable-timestamp fallback path below.
}

func TestSaveTransactionFallsBackOnUnparseableTimestamp(t *testing.T) {
	db := connectTestDB(t)

	tx := portfolio.Transaction{
		Symbol: "TESTTXBADTS", Type: "SELL", Quantity: 1, Price: 10, Timestamp: "not-a-timestamp",
	}
	if err := db.SaveTransaction(tx); err != nil {
		t.Fatalf("expected an unparseable timestamp to fall back to now, not error: %v", err)
	}
}
