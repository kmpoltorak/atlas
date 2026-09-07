package portfolio

import (
	"strings"
	"testing"

	"atlas/internal/domain/portfolio"
)

// A backdated trade must land in the history at its own date, not on top of today's position.
// Recording folded the new trade onto the materialized position in entry order while deleting
// replayed the ledger by timestamp, so the same remaining history implied two different cost
// bases depending on which operation you reached it by.
func TestRecordTransactionReplaysABackdatedTradeChronologically(t *testing.T) {
	uc := NewUseCase()
	mustRecord(t, uc, portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-03T00:00:00Z"})
	mustRecord(t, uc, portfolio.Transaction{Symbol: "AAPL", Type: "SELL", Quantity: 5, Price: 120, Timestamp: "2026-01-04T00:00:00Z"})
	mustRecord(t, uc, portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 200, Timestamp: "2026-01-02T00:00:00Z"})

	positions, _ := uc.GetPositions()
	position := positions[findPositionIndex(positions, "AAPL")]
	// Jan 2 BUY 10@200, Jan 3 BUY 10@100 → 20 @ 150, Jan 4 SELL 5 → 15 @ 150.
	if position.Quantity != 15 || position.AverageCost != 150 {
		t.Fatalf("expected 15 shares at a chronological weighted average of 150, got %+v", position)
	}
}

// The same history reached by deleting rather than by inserting must produce the same position.
func TestAddingAndDeletingAgreeOnTheSameRemainingHistory(t *testing.T) {
	inserted := NewUseCase()
	mustRecord(t, inserted, portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-03T00:00:00Z"})
	mustRecord(t, inserted, portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 200, Timestamp: "2026-01-02T00:00:00Z"})

	deleted := NewUseCase()
	mustRecord(t, deleted, portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 200, Timestamp: "2026-01-02T00:00:00Z"})
	mustRecord(t, deleted, portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-03T00:00:00Z"})
	mustRecord(t, deleted, portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 7, Price: 999, Timestamp: "2026-01-05T00:00:00Z"})
	if err := deleted.RemoveTransaction(deleted.Transactions[2].ID); err != nil {
		t.Fatalf("remove: %v", err)
	}

	a, _ := inserted.GetPositions()
	b, _ := deleted.GetPositions()
	if a[0].Quantity != b[0].Quantity || a[0].AverageCost != b[0].AverageCost {
		t.Fatalf("insert path gave %+v, delete path gave %+v", a[0], b[0])
	}
}

// Deleting the only BUY leaves a history that never could have happened. Replay must reject it
// rather than flooring the quantity at zero and storing the result.
func TestRemoveTransactionRejectsAnImpossibleRemainingHistory(t *testing.T) {
	uc := NewUseCase()
	mustRecord(t, uc, portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-01T00:00:00Z"})
	mustRecord(t, uc, portfolio.Transaction{Symbol: "AAPL", Type: "SELL", Quantity: 5, Price: 120, Timestamp: "2026-01-02T00:00:00Z"})
	buyID := uc.Transactions[0].ID

	err := uc.RemoveTransaction(buyID)
	if err == nil || !strings.Contains(err.Error(), "cannot sell more than the held quantity") {
		t.Fatalf("expected the removal to be rejected, got %v", err)
	}
	if len(uc.Transactions) != 2 {
		t.Fatalf("a rejected removal must publish nothing, got %d transactions", len(uc.Transactions))
	}
	positions, _ := uc.GetPositions()
	if len(positions) != 1 || positions[0].Quantity != 5 {
		t.Fatalf("expected the position to be untouched at 5 shares, got %+v", positions)
	}
}

// A backdated SELL that oversells at its own point in the history is just as impossible as one
// entered today, even though the *current* quantity would cover it.
func TestRecordTransactionRejectsABackdatedOversell(t *testing.T) {
	uc := NewUseCase()
	mustRecord(t, uc, portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-03T00:00:00Z"})

	err := uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "SELL", Quantity: 4, Price: 120, Timestamp: "2026-01-02T00:00:00Z"})
	if err == nil {
		t.Fatal("expected a SELL dated before the BUY that covers it to be rejected")
	}
	if len(uc.Transactions) != 1 {
		t.Fatalf("a rejected record must publish nothing, got %d transactions", len(uc.Transactions))
	}
}

// Selling a fractional holding in two parts must not be refused by binary rounding error.
func TestReplayToleratesFractionalRoundingWhenSellingEverything(t *testing.T) {
	uc := NewUseCase()
	mustRecord(t, uc, portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 0.3, Price: 100, Timestamp: "2026-01-01T00:00:00Z"})
	mustRecord(t, uc, portfolio.Transaction{Symbol: "AAPL", Type: "SELL", Quantity: 0.1, Price: 120, Timestamp: "2026-01-02T00:00:00Z"})
	mustRecord(t, uc, portfolio.Transaction{Symbol: "AAPL", Type: "SELL", Quantity: 0.2, Price: 120, Timestamp: "2026-01-03T00:00:00Z"})

	if positions, _ := uc.GetPositions(); len(positions) != 0 {
		t.Fatalf("expected the position to be closed, got %+v", positions)
	}
}

func mustRecord(t *testing.T, uc *UseCase, tx portfolio.Transaction) {
	t.Helper()
	if err := uc.RecordTransaction(tx); err != nil {
		t.Fatalf("record %s %v %s: %v", tx.Type, tx.Quantity, tx.Symbol, err)
	}
}
