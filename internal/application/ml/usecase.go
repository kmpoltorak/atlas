package ml

import (
	"math"
	"sync/atomic"

	analyzerUC "atlas/internal/application/analyzer"
	"atlas/internal/domain/market"
	"atlas/internal/domain/ml"
)

// UseCase defines the ML contract boundary for feature generation and prediction. The
// correction and recovery models are both optional and independent (nil-safe, matching
// decision.Orchestrator's ML/Portfolio pattern): when unset, Predict falls back to the
// deterministic RSI-derived stub for correction probability, and leaves RecoveryProbability at
// 0, rather than failing to start — see training/README.md for how to produce a real model and
// NewUseCaseWithModel(s) to wire one/both in.
//
// Both are held as atomic.Pointer, not plain fields: the Account dashboard page lets an admin
// pick a different trained model at runtime (see SetModel/SetRecoveryModel), and Predict runs
// concurrently with that from other in-flight HTTP requests — a plain pointer swap would be a
// data race.
type UseCase struct {
	model         atomic.Pointer[OnnxModel]
	recoveryModel atomic.Pointer[OnnxModel]
}

func NewUseCase() *UseCase {
	return &UseCase{}
}

// NewUseCaseWithModel wires a loaded correction-probability ONNX model in, enabling real
// inference in Predict. Leaves the recovery model unset — see NewUseCaseWithModels to wire both.
func NewUseCaseWithModel(model *OnnxModel) *UseCase {
	u := &UseCase{}
	u.model.Store(model)
	return u
}

// NewUseCaseWithModels wires both the correction-probability and recovery-probability models
// in. Either may be nil independently — a nil recoveryModel behaves exactly like
// NewUseCaseWithModel(model) alone.
func NewUseCaseWithModels(model, recoveryModel *OnnxModel) *UseCase {
	u := &UseCase{}
	u.model.Store(model)
	u.recoveryModel.Store(recoveryModel)
	return u
}

// CurrentModel returns the correction-probability model currently in effect, or nil when none is
// loaded (the deterministic stub applies).
func (u *UseCase) CurrentModel() *OnnxModel {
	return u.model.Load()
}

// CurrentRecoveryModel returns the recovery-probability model currently in effect, or nil.
func (u *UseCase) CurrentRecoveryModel() *OnnxModel {
	return u.recoveryModel.Load()
}

// SetModel swaps in a new correction-probability model (nil disables it, falling back to the
// stub) and returns whichever model was active before the swap, so the caller can Close() it —
// this package never closes a model out from under a request that's still using it, since the
// swap itself is atomic, but the old session's resources still need releasing once no new
// requests can observe it. Used by the Account page's model-activation flow.
func (u *UseCase) SetModel(model *OnnxModel) *OnnxModel {
	return u.model.Swap(model)
}

// SetRecoveryModel is SetModel's counterpart for the independent recovery-probability model.
func (u *UseCase) SetRecoveryModel(model *OnnxModel) *OnnxModel {
	return u.recoveryModel.Swap(model)
}

// GenerateFeatures returns the provided feature vectors unchanged in the first milestone.
func (u *UseCase) GenerateFeatures(rows []ml.FeatureVector) ([]ml.FeatureVector, error) {
	return rows, nil
}

// BuildTrainingDataset creates a minimal deterministic training sample for the first prototype.
func (u *UseCase) BuildTrainingDataset() ([]ml.FeatureVector, error) {
	return []ml.FeatureVector{
		{RSI: 62, EMA: 0.5, MACD: 0.7, ATR: 1.1, Volume: 1.2},
		{RSI: 35, EMA: 0.4, MACD: 0.2, ATR: 0.9, Volume: 0.8},
	}, nil
}

// Validate returns a placeholder validation score for the current model skeleton.
func (u *UseCase) Validate() (float64, error) {
	return 0.78, nil
}

// BuildFeatureVector derives a feature vector from an analyzer's latest indicator values for a
// symbol. RSI falls back to a neutral 50 when there isn't enough history to compute it.
func BuildFeatureVector(analyzer *analyzerUC.UseCase, symbol string, bars []market.Bar) ml.FeatureVector {
	feature := ml.FeatureVector{RSI: 50}
	if analyzer == nil || len(bars) == 0 {
		return feature
	}
	if series, err := analyzer.ComputeRSI(symbol, bars); err == nil && len(series) > 0 {
		feature.RSI = series[len(series)-1]
	}
	if series, err := analyzer.ComputeEMA(symbol, bars); err == nil && len(series) > 0 {
		feature.EMA = series[len(series)-1]
	}
	if series, err := analyzer.ComputeMACD(symbol, bars); err == nil && len(series) > 0 {
		feature.MACD = series[len(series)-1]
	}
	if series, err := analyzer.ComputeATR(symbol, bars); err == nil && len(series) > 0 {
		feature.ATR = series[len(series)-1]
	}
	if series, err := analyzer.ComputeVolume(symbol, bars); err == nil && len(series) > 0 {
		feature.Volume = series[len(series)-1]
	}
	return feature
}

// Predict runs real ONNX inference when a model is loaded (u.Model != nil), otherwise falls
// back to a deterministic RSI-derived placeholder. A loaded model's inference error is
// returned as-is rather than silently falling back to the stub — a broken model at request
// time should be loud, not masked as a working prediction.
func (u *UseCase) Predict(features ml.FeatureVector) (ml.Prediction, error) {
	model := u.CurrentModel()
	if model == nil {
		return ml.Prediction{
			Score:        features.RSI / 100,
			Confidence:   0.72,
			ModelVersion: ml.StubModelVersion,
		}, nil
	}

	correctionProbability, err := model.PredictCorrectionProbability(features)
	if err != nil {
		return ml.Prediction{}, err
	}

	var recoveryProbability float64
	var recoveryModelVersion string
	if recoveryModel := u.CurrentRecoveryModel(); recoveryModel != nil {
		// Reuses the same PredictCorrectionProbability method: both models are binary XGBoost
		// classifiers with an identical feature layout and ONNX export shape (see
		// training/train.py) — only which label they were trained against differs, not the
		// inference mechanics. A recovery-model inference error is non-fatal here, unlike the
		// correction model's above: recovery is a secondary, always-optional signal (stays 0,
		// its documented default), whereas a broken *correction* model failing loudly matters
		// more since Score/Confidence derive from it directly.
		if p, err := recoveryModel.PredictCorrectionProbability(features); err == nil {
			recoveryProbability = p
			recoveryModelVersion = recoveryModel.Version
		}
	}

	return ml.Prediction{
		// Score stays a bullish-lean signal for decision.Orchestrator.blendMLAndPortfolio's
		// existing >=0.6/<=0.4 thresholds: a high correction probability should read as
		// bearish (low Score), the inverse of the raw model output.
		Score:                 1 - correctionProbability,
		CorrectionProbability: correctionProbability,
		RecoveryProbability:   recoveryProbability,
		RecoveryModelVersion:  recoveryModelVersion,
		// Distance from 0.5 as a confidence proxy: a probability near 0 or 1 is a confident
		// prediction, one near 0.5 is not. No other confidence signal is available from a
		// binary classifier's probability output alone.
		Confidence:   math.Abs(correctionProbability-0.5) * 2,
		ModelVersion: model.Version,
	}, nil
}
