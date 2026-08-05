package decision

import "atlas/internal/domain/decision"

type Engine interface {
	TechnicalScore(score decision.TechnicalScore) (float64, error)
	MLScore() (float64, error)
	RiskScore() (float64, error)
	Recommend(symbol string) (decision.Recommendation, error)
}
