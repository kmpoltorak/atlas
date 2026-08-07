package backtesting

import (
	"testing"

	domainDecision "atlas/internal/domain/decision"
)

func TestSplitWalkForwardRatiosAndOrdering(t *testing.T) {
	bars := uptrendBars(200)

	train, validate, test, err := splitWalkForward(bars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(train)+len(validate)+len(test) != len(bars) {
		t.Fatalf("splits don't cover every bar: %d+%d+%d != %d", len(train), len(validate), len(test), len(bars))
	}
	// 60/20/20 of 200 bars.
	if len(train) != 120 || len(validate) != 40 || len(test) != 40 {
		t.Fatalf("unexpected split sizes: train=%d validate=%d test=%d", len(train), len(validate), len(test))
	}
	// Chronological, non-overlapping: train ends exactly where validate begins, and so on.
	if !train[len(train)-1].Timestamp.Before(validate[0].Timestamp) {
		t.Fatal("expected train to end strictly before validate begins")
	}
	if !validate[len(validate)-1].Timestamp.Before(test[0].Timestamp) {
		t.Fatal("expected validate to end strictly before test begins")
	}
}

func TestSplitWalkForwardRejectsTooSmallWindow(t *testing.T) {
	// 40 bars split 60/20/20 gives a 8-bar test split — below minLookback (15).
	if _, _, _, err := splitWalkForward(uptrendBars(40)); err == nil {
		t.Fatal("expected an error for a window too small to produce three usable splits")
	}
}

func TestOptimizeThresholdsReturnsAValidCandidate(t *testing.T) {
	uc := NewUseCase(newOrchestrator())
	bars := uptrendBars(120)

	best, evaluated, err := uc.optimizeThresholds("AAPL", bars, 10000, "momentum", "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !best.Valid() {
		t.Fatalf("expected a valid threshold set, got %+v", best)
	}
	if evaluated == 0 {
		t.Fatal("expected at least one valid candidate to be evaluated")
	}
}

func TestRunWalkForwardProducesAllThreeSplits(t *testing.T) {
	uc := NewUseCase(newOrchestrator())
	bars := uptrendBars(200)

	result, err := uc.RunWalkForward("AAPL", bars, 10000, "momentum", "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Symbol != "AAPL" {
		t.Fatalf("expected symbol AAPL, got %q", result.Symbol)
	}
	if !result.BestThresholds.Valid() {
		t.Fatalf("expected valid BestThresholds, got %+v", result.BestThresholds)
	}
	if result.CandidatesEvaluated == 0 {
		t.Fatal("expected at least one candidate to have been evaluated")
	}
	if result.Train.StartedAt.IsZero() || result.Validate.StartedAt.IsZero() || result.Test.StartedAt.IsZero() {
		t.Fatal("expected all three splits to have real StartedAt timestamps")
	}
	// Chronological: train's window ends before validate's begins, and so on.
	if !result.Train.FinishedAt.Before(result.Validate.StartedAt) {
		t.Fatalf("expected train to finish before validate starts: train=%v validate=%v", result.Train.FinishedAt, result.Validate.StartedAt)
	}
	if !result.Validate.FinishedAt.Before(result.Test.StartedAt) {
		t.Fatalf("expected validate to finish before test starts: validate=%v test=%v", result.Validate.FinishedAt, result.Test.StartedAt)
	}
}

func TestRunWalkForwardRejectsInvalidInputs(t *testing.T) {
	bars := uptrendBars(200)

	if _, err := NewUseCase(nil).RunWalkForward("AAPL", bars, 10000, "momentum", "v1"); err == nil {
		t.Fatal("expected an error when orchestrator is not configured")
	}
	if _, err := NewUseCase(newOrchestrator()).RunWalkForward("AAPL", bars, 10000, "", "v1"); err == nil {
		t.Fatal("expected an error for missing strategy name")
	}
	if _, err := NewUseCase(newOrchestrator()).RunWalkForward("AAPL", uptrendBars(40), 10000, "momentum", "v1"); err == nil {
		t.Fatal("expected an error for a window too small to split")
	}
}

func TestRunWalkForwardNeverMutatesLiveDecisionThresholds(t *testing.T) {
	orchestrator := newOrchestrator()
	liveDefaults := domainDecision.DefaultThresholds()

	uc := NewUseCase(orchestrator)
	if _, err := uc.RunWalkForward("AAPL", uptrendBars(200), 10000, "momentum", "v1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if orchestrator.Decision.Thresholds != liveDefaults {
		t.Fatalf("expected the live orchestrator's Decision thresholds to be untouched, got %+v", orchestrator.Decision.Thresholds)
	}
}
