package persistence

import (
	"strings"
	"testing"

	"atlas/internal/domain/portfolio"
)

// The application already rejects these (see portfolio.UseCase.RecordTransaction), but the
// database is the only place that also covers a restore, a manual psql session, or a future code
// path that forgets. These assert the constraint exists and bites — a validation that only lives
// in Go is a validation the data can get around.
func TestDatabaseRejectsImpossibleTransactions(t *testing.T) {
	db := connectTestDB(t)
	portfolioID := testPortfolioID(t, db)

	cases := map[string]portfolio.Transaction{
		"negative quantity": {Symbol: "TESTCHK", Type: "BUY", Quantity: -1, Price: 10, PriceInPLN: 10, Timestamp: "2026-08-06T00:00:00Z"},
		"zero quantity":     {Symbol: "TESTCHK", Type: "BUY", Quantity: 0, Price: 10, PriceInPLN: 10, Timestamp: "2026-08-06T00:00:00Z"},
		"negative price":    {Symbol: "TESTCHK", Type: "BUY", Quantity: 1, Price: -10, PriceInPLN: 10, Timestamp: "2026-08-06T00:00:00Z"},
		"zero PLN price":    {Symbol: "TESTCHK", Type: "BUY", Quantity: 1, Price: 10, PriceInPLN: 0, Timestamp: "2026-08-06T00:00:00Z"},
		"unknown type":      {Symbol: "TESTCHK", Type: "SHORT", Quantity: 1, Price: 10, PriceInPLN: 10, Timestamp: "2026-08-06T00:00:00Z"},
	}
	for name, tx := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := db.SaveTransaction(portfolioID, tx); err == nil {
				t.Fatalf("the database accepted a transaction with a %s", name)
			} else if !strings.Contains(err.Error(), "check constraint") {
				t.Fatalf("expected a CHECK constraint violation, got %v", err)
			}
		})
	}
}

func TestDatabaseRejectsImpossiblePositions(t *testing.T) {
	db := connectTestDB(t)
	portfolioID := testPortfolioID(t, db)

	cases := map[string][]portfolio.Position{
		// A fully sold position is deleted, not stored at zero (see applyPositionUpdate).
		"zero quantity":     {{Symbol: "TESTCHKP", Quantity: 0, AverageCost: 10, CurrentPrice: 10}},
		"negative quantity": {{Symbol: "TESTCHKP", Quantity: -5, AverageCost: 10, CurrentPrice: 10}},
		"zero cost basis":   {{Symbol: "TESTCHKP", Quantity: 1, AverageCost: 0, CurrentPrice: 10}},
	}
	for name, positions := range cases {
		t.Run(name, func(t *testing.T) {
			if err := db.SavePositions(portfolioID, positions); err == nil {
				t.Fatalf("the database accepted a position with a %s", name)
			} else if !strings.Contains(err.Error(), "check constraint") {
				t.Fatalf("expected a CHECK constraint violation, got %v", err)
			}
		})
	}
}
