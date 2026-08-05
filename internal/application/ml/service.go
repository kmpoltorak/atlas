package ml

import "atlas/internal/domain/ml"

type Service interface {
	GenerateFeatures(rows []ml.FeatureVector) ([]ml.FeatureVector, error)
	BuildTrainingDataset() ([]ml.FeatureVector, error)
	Validate() (float64, error)
	Predict(features ml.FeatureVector) (ml.Prediction, error)
}
