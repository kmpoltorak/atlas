package decision

import "atlas/internal/domain/decision"

type Service interface {
	ScoreTechnical(input decision.TechnicalScore) (float64, error)
	ScoreML() (float64, error)
	ScoreRisk() (float64, error)
	Recommend(symbol string) (decision.Recommendation, error)
}
