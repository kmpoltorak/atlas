package ml

import (
	analyzerUC "atlas/internal/application/analyzer"
	"atlas/internal/domain/market"
	"atlas/internal/domain/ml"
)

// UseCase defines the first ML contract boundary for feature generation and prediction.
type UseCase struct{}

func NewUseCase() *UseCase {
	return &UseCase{}
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

// Predict returns a deterministic placeholder prediction object.
func (u *UseCase) Predict(features ml.FeatureVector) (ml.Prediction, error) {
	return ml.Prediction{
		Symbol:       "",
		Score:        features.RSI / 100,
		Confidence:   0.72,
		ModelVersion: "stub-v0",
	}, nil
}
