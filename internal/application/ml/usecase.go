package ml

import "atlas/internal/domain/ml"

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

// Predict returns a deterministic placeholder prediction object.
func (u *UseCase) Predict(features ml.FeatureVector) (ml.Prediction, error) {
	return ml.Prediction{
		Symbol:       "",
		Score:        features.RSI / 100,
		Confidence:   0.72,
		ModelVersion: "stub-v0",
	}, nil
}
