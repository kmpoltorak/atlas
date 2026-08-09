package ml

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	domainml "atlas/internal/domain/ml"

	ort "github.com/yalue/onnxruntime_go"
)

// FeatureOrder is the fixed input order both training (training/train.py's FEATURE_ORDER) and
// inference must agree on — the ONNX model has no column names at inference time, only
// positional floats. Kept as a package-level var, not per-model configuration, since
// ml.FeatureVector's shape is fixed in Go regardless of which model is loaded.
//
// These names changed once, from {rsi, ema, macd, atr, volume}, when the three price-unit
// features were replaced by scale-free ratios (see domain/ml.FeatureVector). Models trained
// before that change are correctly rejected by featureOrderMatches below — the rename is what
// makes the guard able to catch them at all, since the values changed meaning rather than
// position.
var FeatureOrder = []string{"rsi", "ema_dist", "macd_pct", "tr_pct", "volume"}

// onnxSession is the subset of *ort.DynamicAdvancedSession this package depends on, documenting
// exactly what OnnxModel relies on. Note this alone doesn't make PredictCorrectionProbability
// unit-testable without the real libonnxruntime shared library: ort.NewTensor/NewEmptyTensor
// (called before Run) require the onnxruntime environment to already be initialized. Real
// coverage for that path is the TEST_ONNXRUNTIME_LIB_PATH-gated integration test (see
// onnx_integration_test.go), matching persistence's TEST_DATABASE_DSN pattern — not a plain
// `go test` unit test.
type onnxSession interface {
	Run(inputs, outputs []ort.Value) error
	Destroy() error
}

// OnnxModel wraps a loaded ONNX correction-probability model (see training/train.py).
type OnnxModel struct {
	session onnxSession
	Version string
}

type modelMetadata struct {
	Version      string   `json:"version"`
	FeatureOrder []string `json:"feature_order"`
}

// LoadOnnxModel loads an ONNX model file and its metadata sidecar (same path with .json instead
// of .onnx — see training/train.py), initializing the onnxruntime environment on first call
// (package-level state in onnxruntime_go; safe to call this more than once per process).
// libPath is the path to the onnxruntime shared library (LIBONNXRUNTIME_PATH); an empty string
// leaves onnxruntime_go's platform default ("onnxruntime.so"/".dll") in place.
func LoadOnnxModel(modelPath, libPath string) (*OnnxModel, error) {
	metadata, err := loadModelMetadata(modelPath)
	if err != nil {
		return nil, err
	}
	if !featureOrderMatches(metadata.FeatureOrder) {
		return nil, fmt.Errorf("model metadata feature_order %v does not match the expected order %v — refusing to load a model trained on a different feature layout", metadata.FeatureOrder, FeatureOrder)
	}

	if libPath != "" {
		ort.SetSharedLibraryPath(libPath)
	}
	if !ort.IsInitialized() {
		if err := ort.InitializeEnvironment(); err != nil {
			return nil, fmt.Errorf("initialize onnxruntime: %w", err)
		}
	}

	session, err := ort.NewDynamicAdvancedSession(modelPath, []string{"features"}, []string{"probabilities"}, nil)
	if err != nil {
		return nil, fmt.Errorf("load onnx model %s: %w", modelPath, err)
	}

	return &OnnxModel{session: session, Version: metadata.Version}, nil
}

func loadModelMetadata(modelPath string) (modelMetadata, error) {
	metadataPath := strings.TrimSuffix(modelPath, ".onnx") + ".json"
	data, err := os.ReadFile(metadataPath)
	if err != nil {
		return modelMetadata{}, fmt.Errorf("read model metadata %s: %w", metadataPath, err)
	}
	var metadata modelMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return modelMetadata{}, fmt.Errorf("parse model metadata %s: %w", metadataPath, err)
	}
	return metadata, nil
}

func featureOrderMatches(order []string) bool {
	if len(order) != len(FeatureOrder) {
		return false
	}
	for i, name := range FeatureOrder {
		if order[i] != name {
			return false
		}
	}
	return true
}

// Close releases the underlying onnxruntime session.
func (m *OnnxModel) Close() error {
	return m.session.Destroy()
}

// PredictCorrectionProbability runs real ONNX inference and returns the model's raw
// correction-probability output (column 1 of the 2-class "probabilities" output, per
// training/train.py's zipmap=False XGBoost export).
func (m *OnnxModel) PredictCorrectionProbability(fv domainml.FeatureVector) (float64, error) {
	input := []float32{float32(fv.RSI), float32(fv.EMADist), float32(fv.MACDPct), float32(fv.TRPct), float32(fv.Volume)}
	inTensor, err := ort.NewTensor(ort.NewShape(1, int64(len(input))), input)
	if err != nil {
		return 0, fmt.Errorf("build input tensor: %w", err)
	}
	defer inTensor.Destroy()

	outTensor, err := ort.NewEmptyTensor[float32](ort.NewShape(1, 2))
	if err != nil {
		return 0, fmt.Errorf("build output tensor: %w", err)
	}
	defer outTensor.Destroy()

	if err := m.session.Run([]ort.Value{inTensor}, []ort.Value{outTensor}); err != nil {
		return 0, fmt.Errorf("run inference: %w", err)
	}

	data := outTensor.GetData()
	if len(data) < 2 {
		return 0, fmt.Errorf("unexpected output shape: got %d values, want 2", len(data))
	}
	return float64(data[1]), nil
}
