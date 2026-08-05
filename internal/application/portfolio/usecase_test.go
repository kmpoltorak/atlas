package portfolio

import (
	"testing"

	"atlas/internal/domain/portfolio"
)

func TestRecordTransactionUpdatesPositionsAndROI(t *testing.T) {
	uc := NewUseCase()

	buy := portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2024-01-01"}
	if err := uc.RecordTransaction(buy); err != nil {
		t.Fatalf("expected buy transaction to be accepted: %v", err)
	}

	buy2 := portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 5, Price: 110, Timestamp: "2024-01-02"}
	if err := uc.RecordTransaction(buy2); err != nil {
		t.Fatalf("expected second buy transaction to be accepted: %v", err)
	}

	positions, err := uc.GetPositions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(positions) != 1 {
		t.Fatalf("expected one position, got %d", len(positions))
	}
	if positions[0].Quantity != 15 {
		t.Fatalf("expected total quantity 15, got %v", positions[0].Quantity)
	}

	roi, err := uc.ComputeROI()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if roi != 6.451612903225811 {
		t.Fatalf("expected weighted ROI 6.451612903225811, got %v", roi)
	}
}

func TestRecordTransactionRejectsInvalidInput(t *testing.T) {
	uc := NewUseCase()
	if err := uc.RecordTransaction(portfolio.Transaction{Symbol: "", Type: "BUY", Quantity: 1, Price: 1}); err == nil {
		t.Fatal("expected invalid symbol to be rejected")
	}
	if err := uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 0, Price: 1}); err == nil {
		t.Fatal("expected zero quantity to be rejected")
	}
}
