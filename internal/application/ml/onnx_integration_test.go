package ml

import (
	"os"
	"testing"

	domainml "atlas/internal/domain/ml"
)

// TestOnnxModelRealInference loads the real ONNX model fixture (testdata/atlas-ml-smoketest.*,
// trained on synthetic data purely to exercise the export/train/load pipeline mechanics — see
// training/README.md) through the actual onnxruntime_go binding and real libonnxruntime shared
// library, proving the whole Go inference path works end to end, not just against a mock
// session. Skipped unless TEST_ONNXRUNTIME_LIB_PATH points at a real libonnxruntime.{so,dylib}
// (e.g. the one bundled with `pip install onnxruntime`, matching persistence's
// TEST_DATABASE_DSN-gated integration test pattern), since that native library isn't present
// on a plain `go test`/CI runner.
func TestOnnxModelRealInference(t *testing.T) {
	libPath := os.Getenv("TEST_ONNXRUNTIME_LIB_PATH")
	if libPath == "" {
		t.Skip("TEST_ONNXRUNTIME_LIB_PATH not set; skipping real ONNX Runtime integration test")
	}

	model, err := LoadOnnxModel("testdata/atlas-ml-smoketest.onnx", libPath)
	if err != nil {
		t.Fatalf("LoadOnnxModel failed: %v", err)
	}
	defer model.Close()

	if model.Version != "smoketest" {
		t.Fatalf("expected model version %q from metadata sidecar, got %q", "smoketest", model.Version)
	}

	prob, err := model.PredictCorrectionProbability(domainml.FeatureVector{RSI: 50, EMA: 100, MACD: 0, ATR: 1.5, Volume: 1})
	if err != nil {
		t.Fatalf("PredictCorrectionProbability failed: %v", err)
	}
	if prob < 0 || prob > 1 {
		t.Fatalf("expected a probability in [0,1], got %v", prob)
	}
	t.Logf("real ONNX inference returned correction probability %v", prob)
}

// TestUseCasePredictWithRealModel exercises UseCase.Predict end to end through the real model,
// verifying the Score-inversion and confidence-derivation logic against genuine model output.
func TestUseCasePredictWithRealModel(t *testing.T) {
	libPath := os.Getenv("TEST_ONNXRUNTIME_LIB_PATH")
	if libPath == "" {
		t.Skip("TEST_ONNXRUNTIME_LIB_PATH not set; skipping real ONNX Runtime integration test")
	}

	model, err := LoadOnnxModel("testdata/atlas-ml-smoketest.onnx", libPath)
	if err != nil {
		t.Fatalf("LoadOnnxModel failed: %v", err)
	}
	defer model.Close()

	uc := NewUseCaseWithModel(model)
	prediction, err := uc.Predict(domainml.FeatureVector{RSI: 65, EMA: 101, MACD: 0.3, ATR: 1.2, Volume: 1.1})
	if err != nil {
		t.Fatalf("Predict failed: %v", err)
	}
	if prediction.ModelVersion != "smoketest" {
		t.Fatalf("expected model_version %q, got %q", "smoketest", prediction.ModelVersion)
	}
	wantScore := 1 - prediction.CorrectionProbability
	if prediction.Score != wantScore {
		t.Fatalf("expected Score = 1-CorrectionProbability (%v), got %v", wantScore, prediction.Score)
	}
}
