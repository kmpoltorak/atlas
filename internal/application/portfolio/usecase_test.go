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

func TestComputeRiskWithNoPositionsIsZero(t *testing.T) {
	uc := NewUseCase()
	risk, err := uc.ComputeRisk()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if risk.Exposure != 0 || risk.Drawdown != 0 || risk.Volatility != 0 {
		t.Fatalf("expected a zero-value risk snapshot with no positions, got %+v", risk)
	}
}

func TestComputeRiskDrawdownReflectsOnlyLosingPositions(t *testing.T) {
	uc := NewUseCase()
	uc.Positions = []portfolio.Position{
		{Symbol: "WINNER", Quantity: 10, AverageCost: 100, CurrentPrice: 120}, // +200 unrealized
		{Symbol: "LOSER", Quantity: 10, AverageCost: 100, CurrentPrice: 80},   // -200 unrealized
	}

	risk, err := uc.ComputeRisk()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantExposure := 120*10 + 80*10.0 // 2000
	if risk.Exposure != wantExposure {
		t.Fatalf("expected exposure %v, got %v", wantExposure, risk.Exposure)
	}
	// Only LOSER's -200 counts toward drawdown, not WINNER's +200 — drawdown is downside-only.
	wantDrawdown := -200.0 / wantExposure
	if risk.Drawdown != wantDrawdown {
		t.Fatalf("expected drawdown %v, got %v", wantDrawdown, risk.Drawdown)
	}
}

func TestComputeRiskDrawdownIsZeroWithNoLosingPositions(t *testing.T) {
	uc := NewUseCase()
	uc.Positions = []portfolio.Position{
		{Symbol: "WINNER", Quantity: 10, AverageCost: 100, CurrentPrice: 120},
	}
	risk, err := uc.ComputeRisk()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if risk.Drawdown != 0 {
		t.Fatalf("expected zero drawdown with only winning positions, got %v", risk.Drawdown)
	}
}

func TestRemovePositionDeletesIt(t *testing.T) {
	uc := NewUseCase()
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2024-01-01"})
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "MSFT", Type: "BUY", Quantity: 5, Price: 300, Timestamp: "2024-01-01"})

	if err := uc.RemovePosition("AAPL"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	positions, _ := uc.GetPositions()
	if len(positions) != 1 || positions[0].Symbol != "MSFT" {
		t.Fatalf("expected only MSFT to remain, got %+v", positions)
	}
	// The transaction history is an append-only audit log — removing the position must not
	// erase what actually happened.
	if len(uc.Transactions) != 2 {
		t.Fatalf("expected transaction history to be untouched, got %d transactions", len(uc.Transactions))
	}
}

func TestRemovePositionForUnknownSymbolIsANoop(t *testing.T) {
	uc := NewUseCase()
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2024-01-01"})

	if err := uc.RemovePosition("MSFT"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	positions, _ := uc.GetPositions()
	if len(positions) != 1 || positions[0].Symbol != "AAPL" {
		t.Fatalf("expected AAPL to remain untouched, got %+v", positions)
	}
}

func TestRemovePositionRejectsEmptySymbol(t *testing.T) {
	uc := NewUseCase()
	if err := uc.RemovePosition(""); err == nil {
		t.Fatal("expected error for empty symbol")
	}
}

func TestRecordTransactionAssignsAnID(t *testing.T) {
	uc := NewUseCase()
	tx := portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-01T00:00:00Z"}
	if err := uc.RecordTransaction(tx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uc.Transactions) != 1 || uc.Transactions[0].ID == "" {
		t.Fatalf("expected a non-empty generated id, got %+v", uc.Transactions)
	}
}

func TestGetTransactionsReturnsRecorded(t *testing.T) {
	uc := NewUseCase()
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-01T00:00:00Z"})

	transactions, err := uc.GetTransactions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(transactions) != 1 || transactions[0].Symbol != "AAPL" {
		t.Fatalf("expected one AAPL transaction, got %+v", transactions)
	}
}

// TestRemoveTransactionRecomputesPosition proves the position is rebuilt from the remaining
// transactions, not patched in place: removing the middle SELL of a BUY/SELL/BUY sequence must
// produce the same weighted-average cost as if that SELL had never happened, not whatever an
// incremental "undo" of the SELL's quantity delta would leave behind.
func TestRemoveTransactionRecomputesPosition(t *testing.T) {
	uc := NewUseCase()
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-01T00:00:00Z"})
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "SELL", Quantity: 4, Price: 120, Timestamp: "2026-01-02T00:00:00Z"})
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 5, Price: 130, Timestamp: "2026-01-03T00:00:00Z"})

	sellID := uc.Transactions[1].ID
	if err := uc.RemoveTransaction(sellID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(uc.Transactions) != 2 {
		t.Fatalf("expected the SELL to be gone from history, got %+v", uc.Transactions)
	}
	positions, _ := uc.GetPositions()
	if len(positions) != 1 {
		t.Fatalf("expected one AAPL position, got %+v", positions)
	}
	wantQuantity, wantAvgCost := 15.0, (100*10.0+130*5.0)/15.0
	if positions[0].Quantity != wantQuantity {
		t.Fatalf("expected quantity %v (as if the SELL never happened), got %v", wantQuantity, positions[0].Quantity)
	}
	if positions[0].AverageCost != wantAvgCost {
		t.Fatalf("expected average cost %v, got %v", wantAvgCost, positions[0].AverageCost)
	}
}

func TestRemoveTransactionForUnknownIDIsANoop(t *testing.T) {
	uc := NewUseCase()
	_ = uc.RecordTransaction(portfolio.Transaction{Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-01T00:00:00Z"})

	if err := uc.RemoveTransaction("does-not-exist"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uc.Transactions) != 1 {
		t.Fatalf("expected the existing transaction to be untouched, got %+v", uc.Transactions)
	}
}

func TestRemoveTransactionRejectsEmptyID(t *testing.T) {
	uc := NewUseCase()
	if err := uc.RemoveTransaction(""); err == nil {
		t.Fatal("expected error for empty id")
	}
}
