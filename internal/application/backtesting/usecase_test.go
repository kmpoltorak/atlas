package backtesting

import "testing"

func TestRunReturnsDeterministicBacktestSummary(t *testing.T) {
	uc := NewUseCase()
	result, err := uc.Run("momentum", "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StrategyName != "momentum" {
		t.Fatalf("expected strategy name momentum, got %q", result.StrategyName)
	}
	if result.Trades != 12 {
		t.Fatalf("expected 12 trades, got %d", result.Trades)
	}
}

func TestRunRejectsMissingStrategyName(t *testing.T) {
	uc := NewUseCase()
	if _, err := uc.Run("", "v1"); err == nil {
		t.Fatal("expected missing strategy name to be rejected")
	}
}
