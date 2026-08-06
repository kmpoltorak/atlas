package ml

import (
	"math"

	analyzerUC "atlas/internal/application/analyzer"
	"atlas/internal/domain/market"
	"atlas/internal/domain/ml"
)

// UseCase defines the ML contract boundary for feature generation and prediction. Model is
// optional (nil-safe, matching decision.Orchestrator's ML/Portfolio pattern): when unset,
// Predict falls back to the deterministic RSI-derived stub rather than failing to start —
// see training/README.md for how to produce a real model and NewUseCaseWithModel to wire it in.
type UseCase struct {
	Model *OnnxModel
}

func NewUseCase() *UseCase {
	return &UseCase{}
}

// NewUseCaseWithModel wires a loaded ONNX model in, enabling real correction-probability
// inference in Predict.
func NewUseCaseWithModel(model *OnnxModel) *UseCase {
	return &UseCase{Model: model}
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
	if u.Model == nil {
		return ml.Prediction{
			Score:        features.RSI / 100,
			Confidence:   0.72,
			ModelVersion: ml.StubModelVersion,
		}, nil
	}

	correctionProbability, err := u.Model.PredictCorrectionProbability(features)
	if err != nil {
		return ml.Prediction{}, err
	}
	return ml.Prediction{
		// Score stays a bullish-lean signal for decision.Orchestrator.blendMLAndPortfolio's
		// existing >=0.6/<=0.4 thresholds: a high correction probability should read as
		// bearish (low Score), the inverse of the raw model output.
		Score:                 1 - correctionProbability,
		CorrectionProbability: correctionProbability,
		// Distance from 0.5 as a confidence proxy: a probability near 0 or 1 is a confident
		// prediction, one near 0.5 is not. No other confidence signal is available from a
		// binary classifier's probability output alone.
		Confidence:   math.Abs(correctionProbability-0.5) * 2,
		ModelVersion: u.Model.Version,
	}, nil
}
