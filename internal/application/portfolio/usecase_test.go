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
