package ml

import (
	"os"
	"strconv"
	"strings"
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

	prob, err := model.PredictCorrectionProbability(domainml.FeatureVector{RSI: 50, EMADist: 0.0, MACDPct: 0.0, TRPct: 0.015, Volume: 1})
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

	uc := NewUseCaseWithModels(model, nil)
	prediction, err := uc.Predict(domainml.FeatureVector{RSI: 65, EMADist: 0.01, MACDPct: 0.003, TRPct: 0.012, Volume: 1.1})
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

// TestUseCasePredictWithRecoveryModelPopulatesRecoveryProbability proves the second, independent
// model wired via NewUseCaseWithModels actually flows into RecoveryProbability — reusing the same
// smoketest fixture for both roles since this only exercises the wiring, not real recovery
// accuracy (that's covered by training/train.py's own printed/persisted holdout metrics, not a
// Go unit test).
func TestUseCasePredictWithRecoveryModelPopulatesRecoveryProbability(t *testing.T) {
	libPath := os.Getenv("TEST_ONNXRUNTIME_LIB_PATH")
	if libPath == "" {
		t.Skip("TEST_ONNXRUNTIME_LIB_PATH not set; skipping real ONNX Runtime integration test")
	}

	model, err := LoadOnnxModel("testdata/atlas-ml-smoketest.onnx", libPath)
	if err != nil {
		t.Fatalf("LoadOnnxModel failed: %v", err)
	}
	defer model.Close()

	recoveryModel, err := LoadOnnxModel("testdata/atlas-ml-smoketest.onnx", libPath)
	if err != nil {
		t.Fatalf("LoadOnnxModel (recovery) failed: %v", err)
	}
	defer recoveryModel.Close()

	uc := NewUseCaseWithModels(model, recoveryModel)
	prediction, err := uc.Predict(domainml.FeatureVector{RSI: 65, EMADist: 0.01, MACDPct: 0.003, TRPct: 0.012, Volume: 1.1})
	if err != nil {
		t.Fatalf("Predict failed: %v", err)
	}
	if prediction.RecoveryProbability < 0 || prediction.RecoveryProbability > 1 {
		t.Fatalf("expected RecoveryProbability in [0,1], got %v", prediction.RecoveryProbability)
	}
	if prediction.RecoveryProbability == 0 {
		t.Fatal("expected a real (non-default-zero) RecoveryProbability from the loaded recovery model")
	}
}

// TestUseCasePredictWithoutRecoveryModelLeavesRecoveryProbabilityZero proves the documented
// default still holds when only the correction model is loaded (NewUseCaseWithModels with a nil
// recoveryModel, matching existing callers that never mention recovery at all).
func TestUseCasePredictWithoutRecoveryModelLeavesRecoveryProbabilityZero(t *testing.T) {
	libPath := os.Getenv("TEST_ONNXRUNTIME_LIB_PATH")
	if libPath == "" {
		t.Skip("TEST_ONNXRUNTIME_LIB_PATH not set; skipping real ONNX Runtime integration test")
	}

	model, err := LoadOnnxModel("testdata/atlas-ml-smoketest.onnx", libPath)
	if err != nil {
		t.Fatalf("LoadOnnxModel failed: %v", err)
	}
	defer model.Close()

	uc := NewUseCaseWithModels(model, nil)
	prediction, err := uc.Predict(domainml.FeatureVector{RSI: 65, EMADist: 0.01, MACDPct: 0.003, TRPct: 0.012, Volume: 1.1})
	if err != nil {
		t.Fatalf("Predict failed: %v", err)
	}
	if prediction.RecoveryProbability != 0 {
		t.Fatalf("expected RecoveryProbability to stay 0 without a recovery model loaded, got %v", prediction.RecoveryProbability)
	}
}

// TestLoadOnnxModelRejectsAGraphNarrowerThanItsSidecarClaims pins the bug that made this whole
// file worth wiring into CI. testdata/atlas-ml-stale-graph.* is a graph built for the old
// 5-feature layout carrying a sidecar that claims the current 8 — so featureOrderMatches, which
// only ever reads the sidecar, waves it through. Before graphFeatureWidthMatches existed, such a
// model loaded cleanly and then failed every inference at runtime with an opaque "Got invalid
// dimensions for input" from onnxruntime; in production that would be a startup that looks
// healthy and a prediction path that is dead. The load must fail here instead, naming both widths.
func TestLoadOnnxModelRejectsAGraphNarrowerThanItsSidecarClaims(t *testing.T) {
	libPath := os.Getenv("TEST_ONNXRUNTIME_LIB_PATH")
	if libPath == "" {
		t.Skip("TEST_ONNXRUNTIME_LIB_PATH not set; skipping real ONNX Runtime integration test")
	}

	model, err := LoadOnnxModel("testdata/atlas-ml-stale-graph.onnx", libPath)
	if err == nil {
		model.Close()
		t.Fatal("expected a load error for a graph declaring fewer features than FeatureOrder, got none")
	}
	for _, want := range []string{"5", strconv.Itoa(len(FeatureOrder)), "feature layout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q so the fix is obvious from the message alone", err, want)
		}
	}
}
