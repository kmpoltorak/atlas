package ml

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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
var FeatureOrder = []string{"rsi", "ema_dist", "macd_pct", "tr_pct", "atr_pct", "rvol_20", "rvol_50", "volume"}

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
	mu      sync.RWMutex
	closed  bool
	session onnxSession
	Version string
	// Filename is the file this model was loaded from, relative to ML_MODELS_DIR. It is what the
	// durable Account-page selection records, so it is also how a replica answers "am I already
	// serving the selected model" without reloading it to compare versions.
	Filename        string
	TrainingDataEnd time.Time
	// labelParams is the correction label the model was trained against, from its metadata's
	// label_params; nil when the metadata predates that field. See LabelParams.
	labelParams *CorrectionLabelParams
}

type modelMetadata struct {
	Version         string               `json:"version"`
	FeatureOrder    []string             `json:"feature_order"`
	TrainingDataEnd string               `json:"train_label_end"`
	LabelParams     *metadataLabelParams `json:"label_params"`
}

// metadataLabelParams is the label_params object training/train.py copies from
// cmd/export-training-data's sidecar (see writeLabelParams there).
type metadataLabelParams struct {
	LabelType          string  `json:"label_type"`
	HorizonBars        int     `json:"horizon_bars"`
	ThresholdPct       float64 `json:"threshold_pct"`
	VolatilityMultiple float64 `json:"volatility_multiple"`
	VolatilityWindow   int     `json:"volatility_window"`
	MinThresholdPct    float64 `json:"min_threshold_pct"`
}

// correctionParams returns the correction label the metadata describes, or nil when it has none
// or it is not usable as one (no horizon).
func (m *metadataLabelParams) correctionParams() *CorrectionLabelParams {
	if m == nil || m.HorizonBars <= 0 {
		return nil
	}
	return &CorrectionLabelParams{
		HorizonBars: m.HorizonBars, ThresholdPct: m.ThresholdPct,
		VolatilityMultiple: m.VolatilityMultiple, VolatilityWindow: m.VolatilityWindow,
		MinThresholdPct: m.MinThresholdPct,
	}
}

// LabelParams is the correction label to judge this model against, and whether that came from
// the model itself.
//
// The exporter accepts a custom horizon and barrier, and the model records the ones it was
// trained on. Scoring it against DefaultCorrectionLabelParams regardless measured it against a
// different event than the one it learned, with nothing to say so. A model whose metadata
// predates label_params cannot say what it learned; it falls back to the defaults, and fromModel
// is false so the caller can report that rather than present the result as a like-for-like
// measurement.
func (m *OnnxModel) LabelParams() (params CorrectionLabelParams, fromModel bool) {
	if m != nil && m.labelParams != nil {
		return *m.labelParams, true
	}
	return DefaultCorrectionLabelParams(), false
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

	if err := graphFeatureWidthMatches(modelPath); err != nil {
		return nil, err
	}

	session, err := ort.NewDynamicAdvancedSession(modelPath, []string{"features"}, []string{"probabilities"}, nil)
	if err != nil {
		return nil, fmt.Errorf("load onnx model %s: %w", modelPath, err)
	}

	var trainingEnd time.Time
	if metadata.TrainingDataEnd != "" {
		trainingEnd, err = time.Parse(time.RFC3339Nano, metadata.TrainingDataEnd)
		if err != nil {
			_ = session.Destroy()
			return nil, fmt.Errorf("invalid train_label_end: %w", err)
		}
	}
	return &OnnxModel{
		session: session, Version: metadata.Version,
		Filename: filepath.Base(modelPath), TrainingDataEnd: trainingEnd,
		labelParams: metadata.LabelParams.correctionParams(),
	}, nil
}

// graphFeatureWidthMatches checks the model graph's own declared input width against FeatureOrder.
//
// The feature_order check above reads the sidecar JSON, which is a claim about the model rather
// than the model itself — regenerate one without the other and it passes while the graph still
// expects the old layout. That is not hypothetical: the committed test fixture spent some time in
// exactly that state (sidecar updated from 5 features to 8, graph left at 5), and because the only
// tests that would have caught it skip without a native onnxruntime, nothing surfaced it. In
// production the same mismatch would have loaded cleanly at startup and then failed every single
// prediction at runtime, which is the worst place to find out.
//
// A nil/absent shape is accepted rather than rejected: some exporters leave the dimension
// symbolic, and refusing to load then would break working models to catch a rarer bug than the
// one this guards.
func graphFeatureWidthMatches(modelPath string) error {
	inputs, _, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return fmt.Errorf("inspect onnx model %s: %w", modelPath, err)
	}
	for _, input := range inputs {
		if input.Name != "features" {
			continue
		}
		dims := input.Dimensions
		if len(dims) == 0 {
			return nil
		}
		// Trailing dimension is the feature width; the leading one is the batch axis, normally
		// symbolic (-1) since train.py exports with FloatTensorType([None, len(FEATURE_ORDER)]).
		width := dims[len(dims)-1]
		if width > 0 && int(width) != len(FeatureOrder) {
			return fmt.Errorf(
				"onnx model %s declares %d input features but this build expects %d (%v) — the model was trained on a different feature layout; retrain it or regenerate the metadata sidecar to match",
				modelPath, width, len(FeatureOrder), FeatureOrder)
		}
		return nil
	}
	return nil
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
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	return m.session.Destroy()
}

// PredictCorrectionProbability runs real ONNX inference and returns the model's raw
// correction-probability output (column 1 of the 2-class "probabilities" output, per
// training/train.py's zipmap=False XGBoost export).
func (m *OnnxModel) PredictCorrectionProbability(fv domainml.FeatureVector) (float64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.closed {
		return 0, errors.New("model is closed")
	}
	input := []float32{
		float32(fv.RSI), float32(fv.EMADist), float32(fv.MACDPct), float32(fv.TRPct),
		float32(fv.ATRPct), float32(fv.RealizedVol20), float32(fv.RealizedVol50), float32(fv.Volume),
	}
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
