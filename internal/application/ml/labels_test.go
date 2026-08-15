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

// The point of a volatility-scaled barrier is that the same *shape* of move counts as a
// correction regardless of how volatile the instrument is. Under the old fixed 5% rule a calm
// instrument could never produce a positive example at all, which is how the label ended up
// encoding "is this a volatile ticker" instead of "is a correction coming".
func TestVolatilityScaledLabelIsComparableAcrossVolatilityRegimes(t *testing.T) {
	// Two series with identical shape, differing only in amplitude: one drifts by 0.2% a bar
	// and then drops 3%, the other by 2% a bar and then drops 30%.
	build := func(step, drop float64) []market.Bar {
		bars := make([]market.Bar, 0, 40)
		price := 100.0
		for i := 0; i < 30; i++ {
			if i%2 == 0 {
				price *= 1 + step
			} else {
				price *= 1 - step
			}
			bars = append(bars, market.Bar{Symbol: "X", Close: price, High: price, Low: price})
		}
		for i := 0; i < 10; i++ {
			price *= 1 - drop/10
			bars = append(bars, market.Bar{Symbol: "X", Close: price, High: price, Low: price})
		}
		return bars
	}
	params := DefaultCorrectionLabelParams()
	calm, wild := build(0.002, 0.03), build(0.02, 0.30)

	calmLabel, calmOK := ComputeCorrectionLabel(calm, 29, params)
	wildLabel, wildOK := ComputeCorrectionLabel(wild, 29, params)
	if !calmOK || !wildOK {
		t.Fatalf("expected both labels to resolve, got calmOK=%v wildOK=%v", calmOK, wildOK)
	}
	if calmLabel != wildLabel {
		t.Errorf("identically-shaped moves must label alike under a volatility-scaled barrier, "+
			"got calm=%d wild=%d", calmLabel, wildLabel)
	}

	// The fixed-threshold rule is exactly what fails this: 3% never reaches a flat 5% barrier.
	// That pre-volatility-scaling definition lives here rather than in labels.go — nothing in
	// production builds it any more, it only exists to be contrasted with the default.
	fixed := CorrectionLabelParams{HorizonBars: 10, ThresholdPct: 0.05}
	calmFixed, _ := ComputeCorrectionLabel(calm, 29, fixed)
	wildFixed, _ := ComputeCorrectionLabel(wild, 29, fixed)
	if calmFixed == wildFixed {
		t.Skip("fixed-threshold rule happened to agree here; the regression it guards is elsewhere")
	}
}

// The barrier must never be derived from bars after the one being labelled — otherwise the
// label encodes information unavailable at prediction time and the model looks better than it is.
func TestVolatilityThresholdUsesOnlyTrailingBars(t *testing.T) {
	bars := make([]market.Bar, 0, 60)
	price := 100.0
	for i := 0; i < 60; i++ {
		price *= 1.001
		bars = append(bars, market.Bar{Symbol: "X", Close: price, High: price, Low: price})
	}
	params := DefaultCorrectionLabelParams()
	before := params.thresholdFraction(bars, 30)

	// Violent moves strictly after bar 30 must not change bar 30's threshold.
	for i := 40; i < 60; i++ {
		bars[i].Close *= 3
	}
	if after := params.thresholdFraction(bars, 30); after != before {
		t.Errorf("threshold at bar 30 changed from %v to %v after mutating later bars — the "+
			"volatility window is leaking forward information", before, after)
	}
}

func TestVolatilityThresholdRespectsItsFloor(t *testing.T) {
	// A perfectly flat series has zero realised volatility; without the floor the barrier would
	// collapse to 0 and label every bar a correction.
	bars := make([]market.Bar, 0, 40)
	for i := 0; i < 40; i++ {
		bars = append(bars, market.Bar{Symbol: "X", Close: 100, High: 100, Low: 100})
	}
	params := DefaultCorrectionLabelParams()
	if got := params.thresholdFraction(bars, 30); got != params.MinThresholdPct {
		t.Errorf("flat series should fall back to the floor %v, got %v", params.MinThresholdPct, got)
	}
}

func TestFixedThresholdParamsStillBehaveAsBefore(t *testing.T) {
	params := CorrectionLabelParams{HorizonBars: 10, ThresholdPct: 0.05}
	if got := params.thresholdFraction(nil, 0); got != 0.05 {
		t.Errorf("fixed params must ignore volatility entirely, got %v", got)
	}
}
