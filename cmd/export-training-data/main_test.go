package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mlUC "atlas/internal/application/ml"
	"atlas/internal/domain/market"
)

// The 2026-09-07 review's first finding: the exporter built CorrectionLabelParams from
// HorizonBars and ThresholdPct alone, leaving VolatilityMultiple at zero — the switch that turns
// the barrier back into a fixed 5%. Everything downstream (the decision engine, the backtest's
// ML evaluation, the metadata sidecar's label_definition) describes the volatility-scaled
// barrier, so the model was trained on one event and judged on another.
//
// The defaults the exporter uses must be the defaults the evaluator uses, field for field.
func TestExportedLabelParamsMatchTheEvaluatedOnes(t *testing.T) {
	want := mlUC.DefaultCorrectionLabelParams() // what backtesting.evaluateML scores against
	got := buildCorrectionParams(want.HorizonBars, want.ThresholdPct, want.VolatilityMultiple, want.VolatilityWindow, want.MinThresholdPct)

	if got != want {
		t.Fatalf("the exporter would label a different event than the one Atlas evaluates:\n exported: %+v\nevaluated: %+v", got, want)
	}
	if got.VolatilityMultiple == 0 {
		t.Fatal("VolatilityMultiple 0 silently reverts the barrier to a fixed percentage")
	}
}

// The barrier the exporter labels against must actually differ from the fixed-threshold one —
// otherwise the test above passes on a struct that behaves identically anyway.
func TestVolatilityScaledLabelDiffersFromTheFixedOne(t *testing.T) {
	bars := decliningBars(60)
	scaled := buildCorrectionParams(10, 0.05, 4.0, 20, 0.02)
	fixed := buildCorrectionParams(10, 0.05, 0, 20, 0.02)

	differing := 0
	for i := 25; i < len(bars)-15; i++ {
		a, okA := mlUC.ComputeCorrectionLabel(bars, i, scaled)
		b, okB := mlUC.ComputeCorrectionLabel(bars, i, fixed)
		if okA && okB && a != b {
			differing++
		}
	}
	if differing == 0 {
		t.Fatal("the two barriers labelled every bar identically; this test cannot detect a regression")
	}
}

// A model's metadata has to state the label it was really trained on, which means the parameters
// have to leave the exporter with the CSV.
func TestLabelParamsSidecarRecordsWhatWasExported(t *testing.T) {
	csvPath := filepath.Join(t.TempDir(), "features.csv")
	params := buildCorrectionParams(10, 0.05, 4.0, 20, 0.02)

	if err := writeLabelParams(csvPath, "correction", params, mlUC.RecoveryLabelParams{}); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}
	raw, err := os.ReadFile(labelParamsPath(csvPath))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("sidecar is not valid JSON: %v", err)
	}
	if decoded["volatility_multiple"] != 4.0 || decoded["min_threshold_pct"] != 0.02 {
		t.Fatalf("the sidecar must carry the real barrier, got %v", decoded)
	}
}

// decliningBars is a series that drifts down with alternating step sizes, so the volatility-scaled
// barrier and the fixed one disagree on which drops count.
func decliningBars(n int) []market.Bar {
	bars := make([]market.Bar, 0, n)
	price := 100.0
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			price *= 0.985
		} else {
			price *= 1.005
		}
		bars = append(bars, market.Bar{
			Symbol: "TEST", Open: price, High: price * 1.004, Low: price * 0.996, Close: price, Volume: 1000,
		})
	}
	return bars
}
