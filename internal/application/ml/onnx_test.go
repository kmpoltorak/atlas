package ml

import (
	"os"
	"path/filepath"
	"testing"
)

func writeMetadataFixture(t *testing.T, dir string, content string) string {
	t.Helper()
	path := filepath.Join(dir, "model.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write metadata fixture: %v", err)
	}
	return filepath.Join(dir, "model.onnx")
}

func TestLoadModelMetadataParsesSidecarJSON(t *testing.T) {
	modelPath := writeMetadataFixture(t, t.TempDir(),
		`{"version":"v1","feature_order":["rsi","ema_dist","macd_pct","tr_pct","atr_pct","rvol_20","rvol_50","volume"]}`)

	metadata, err := loadModelMetadata(modelPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metadata.Version != "v1" {
		t.Fatalf("expected version v1, got %q", metadata.Version)
	}
	if !featureOrderMatches(metadata.FeatureOrder) {
		t.Fatalf("expected feature order to match, got %v", metadata.FeatureOrder)
	}
}

func TestLoadModelMetadataErrorsWhenSidecarMissing(t *testing.T) {
	if _, err := loadModelMetadata(filepath.Join(t.TempDir(), "missing.onnx")); err == nil {
		t.Fatal("expected an error for a missing metadata sidecar")
	}
}

func TestFeatureOrderMatchesRejectsWrongOrderOrLength(t *testing.T) {
	if featureOrderMatches([]string{"ema_dist", "rsi", "macd_pct", "tr_pct", "atr_pct", "rvol_20", "rvol_50", "volume"}) {
		t.Fatal("expected a reordered feature list to be rejected")
	}
	if featureOrderMatches([]string{"rsi", "ema_dist", "macd_pct", "tr_pct", "atr_pct", "rvol_20", "rvol_50"}) {
		t.Fatal("expected a short feature list to be rejected")
	}
	if !featureOrderMatches([]string{"rsi", "ema_dist", "macd_pct", "tr_pct", "atr_pct", "rvol_20", "rvol_50", "volume"}) {
		t.Fatal("expected the exact expected order to match")
	}
}

func TestLoadOnnxModelRejectsMismatchedFeatureOrder(t *testing.T) {
	modelPath := writeMetadataFixture(t, t.TempDir(),
		`{"version":"v1","feature_order":["ema_dist","rsi","macd_pct","tr_pct","atr_pct","rvol_20","rvol_50","volume"]}`)

	if _, err := LoadOnnxModel(modelPath, ""); err == nil {
		t.Fatal("expected LoadOnnxModel to refuse a model trained on a different feature layout")
	}
}

// A model exported with a non-default horizon and barrier must be judged on that label, not on
// DefaultCorrectionLabelParams — otherwise backtesting scores it against an event it never learned.
func TestModelLabelParamsComeFromItsMetadata(t *testing.T) {
	dir := t.TempDir()
	modelPath := filepath.Join(dir, "atlas-ml-vcustom.onnx")
	metadata := `{"version":"vcustom","feature_order":[],"label_params":{"label_type":"correction",` +
		`"horizon_bars":5,"threshold_pct":0.03,"volatility_multiple":0,"volatility_window":20,"min_threshold_pct":0.02}}`
	if err := os.WriteFile(filepath.Join(dir, "atlas-ml-vcustom.json"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadModelMetadata(modelPath)
	if err != nil {
		t.Fatalf("load metadata: %v", err)
	}
	model := &OnnxModel{labelParams: loaded.LabelParams.correctionParams()}

	params, fromModel := model.LabelParams()
	if !fromModel {
		t.Fatal("a model that records its label must be judged on it")
	}
	want := CorrectionLabelParams{HorizonBars: 5, ThresholdPct: 0.03, VolatilityWindow: 20, MinThresholdPct: 0.02}
	if params != want {
		t.Fatalf("expected the model's own label %+v, got %+v", want, params)
	}
	if params == DefaultCorrectionLabelParams() {
		t.Fatal("the test needs parameters that differ from the defaults")
	}
}

// Metadata written before label_params existed says nothing about the label; the defaults are
// used and flagged as such rather than presented as the model's own.
func TestModelWithoutLabelParamsFallsBackVisibly(t *testing.T) {
	params, fromModel := (&OnnxModel{}).LabelParams()
	if fromModel || params != DefaultCorrectionLabelParams() {
		t.Fatalf("expected flagged defaults, got %+v fromModel=%v", params, fromModel)
	}
}
