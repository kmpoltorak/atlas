package ml

import (
	"testing"
	"time"

	"atlas/internal/domain/market"
)

func barsWithCloses(closes ...float64) []market.Bar {
	bars := make([]market.Bar, len(closes))
	for i, c := range closes {
		bars[i] = market.Bar{Symbol: "TEST", Close: c, Timestamp: time.Now().Add(time.Duration(i) * time.Hour)}
	}
	return bars
}

func TestComputeCorrectionLabelDetectsDrop(t *testing.T) {
	params := CorrectionLabelParams{HorizonBars: 3, ThresholdPct: 0.05}
	bars := barsWithCloses(100, 100, 100, 94, 100) // bar 3 is a 6% drop from bar 0's 100
	label, ok := ComputeCorrectionLabel(bars, 0, params)
	if !ok {
		t.Fatal("expected ok=true with enough forward history")
	}
	if label != 1 {
		t.Fatalf("expected label 1 for a >5%% drop within the horizon, got %d", label)
	}
}

func TestComputeCorrectionLabelNoDrop(t *testing.T) {
	params := CorrectionLabelParams{HorizonBars: 3, ThresholdPct: 0.05}
	bars := barsWithCloses(100, 101, 99, 97, 100) // never drops below 95 (5% of 100)
	label, ok := ComputeCorrectionLabel(bars, 0, params)
	if !ok {
		t.Fatal("expected ok=true with enough forward history")
	}
	if label != 0 {
		t.Fatalf("expected label 0, got %d", label)
	}
}

func TestComputeCorrectionLabelInsufficientForwardHistoryIsNotOk(t *testing.T) {
	params := CorrectionLabelParams{HorizonBars: 3, ThresholdPct: 0.05}
	bars := barsWithCloses(100, 90, 90) // only 2 bars after index 0, horizon needs 3
	if _, ok := ComputeCorrectionLabel(bars, 0, params); ok {
		t.Fatal("expected ok=false when there isn't enough forward history to evaluate the label")
	}
}

func TestComputeCorrectionLabelRejectsOutOfRangeIndex(t *testing.T) {
	params := DefaultCorrectionLabelParams()
	bars := barsWithCloses(100, 101, 102)
	if _, ok := ComputeCorrectionLabel(bars, -1, params); ok {
		t.Fatal("expected ok=false for a negative index")
	}
	if _, ok := ComputeCorrectionLabel(bars, len(bars), params); ok {
		t.Fatal("expected ok=false for an out-of-range index")
	}
}

func TestComputeCorrectionLabelRejectsZeroBaseClose(t *testing.T) {
	params := CorrectionLabelParams{HorizonBars: 1, ThresholdPct: 0.05}
	bars := barsWithCloses(0, 100)
	if _, ok := ComputeCorrectionLabel(bars, 0, params); ok {
		t.Fatal("expected ok=false for a zero/invalid base close")
	}
}

func TestComputeRecoveryLabelDetectsRecovery(t *testing.T) {
	params := RecoveryLabelParams{
		CorrectionParams:    CorrectionLabelParams{HorizonBars: 3, ThresholdPct: 0.05},
		RecoveryHorizonBars: 3,
	}
	// bar 1 (94) breaches the 5% threshold from bar 0's 100; bar 4 (101) recovers back to
	// >= 100 within 3 bars of the breach.
	bars := barsWithCloses(100, 94, 96, 98, 101)
	label, ok := ComputeRecoveryLabel(bars, 0, params)
	if !ok {
		t.Fatal("expected ok=true with enough forward history")
	}
	if label != 1 {
		t.Fatalf("expected label 1 for a recovery within the horizon, got %d", label)
	}
}

func TestComputeRecoveryLabelNoRecovery(t *testing.T) {
	params := RecoveryLabelParams{
		CorrectionParams:    CorrectionLabelParams{HorizonBars: 3, ThresholdPct: 0.05},
		RecoveryHorizonBars: 3,
	}
	bars := barsWithCloses(100, 94, 96, 98, 99) // never gets back to 100
	label, ok := ComputeRecoveryLabel(bars, 0, params)
	if !ok {
		t.Fatal("expected ok=true with enough forward history")
	}
	if label != 0 {
		t.Fatalf("expected label 0 for no recovery within the horizon, got %d", label)
	}
}

func TestComputeRecoveryLabelNotOkWithoutACorrection(t *testing.T) {
	params := DefaultRecoveryLabelParams()
	bars := barsWithCloses(100, 101, 102, 103, 104, 105, 106) // never drops — no correction to recover from
	if _, ok := ComputeRecoveryLabel(bars, 0, params); ok {
		t.Fatal("expected ok=false when no correction occurred at the base index")
	}
}

func TestComputeRecoveryLabelInsufficientForwardHistoryIsNotOk(t *testing.T) {
	params := RecoveryLabelParams{
		CorrectionParams:    CorrectionLabelParams{HorizonBars: 2, ThresholdPct: 0.05},
		RecoveryHorizonBars: 5,
	}
	// The correction itself is confirmed (breach at bar 1), but only 1 bar exists after the
	// breach — nowhere near the 5-bar recovery horizon.
	bars := barsWithCloses(100, 94, 96)
	if _, ok := ComputeRecoveryLabel(bars, 0, params); ok {
		t.Fatal("expected ok=false when there isn't enough forward history past the breach to know")
	}
}
