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
		`{"version":"v1","feature_order":["rsi","ema_dist","macd_pct","tr_pct","volume"]}`)

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
	if featureOrderMatches([]string{"ema_dist", "rsi", "macd_pct", "tr_pct", "volume"}) {
		t.Fatal("expected a reordered feature list to be rejected")
	}
	if featureOrderMatches([]string{"rsi", "ema_dist", "macd_pct", "tr_pct"}) {
		t.Fatal("expected a short feature list to be rejected")
	}
	if !featureOrderMatches([]string{"rsi", "ema_dist", "macd_pct", "tr_pct", "volume"}) {
		t.Fatal("expected the exact expected order to match")
	}
}

func TestLoadOnnxModelRejectsMismatchedFeatureOrder(t *testing.T) {
	modelPath := writeMetadataFixture(t, t.TempDir(),
		`{"version":"v1","feature_order":["ema_dist","rsi","macd_pct","tr_pct","volume"]}`)

	if _, err := LoadOnnxModel(modelPath, ""); err == nil {
		t.Fatal("expected LoadOnnxModel to refuse a model trained on a different feature layout")
	}
}
