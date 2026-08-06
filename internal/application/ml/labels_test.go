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
