package decision

import "atlas/internal/domain/decision"

// UseCase contains the first deterministic scoring rules for recommendation generation.
type UseCase struct{}

func NewUseCase() *UseCase {
	return &UseCase{}
}

// ScoreTechnical inspects the technical evidence and returns a normalized score.
func (u *UseCase) ScoreTechnical(input decision.TechnicalScore) (float64, error) {
	if input.Overall < 0 {
		return 0, nil
	}
	return input.Overall, nil
}

// ScoreML is intentionally a placeholder for the first ML integration stage.
func (u *UseCase) ScoreML() (float64, error) {
	return 0.65, nil
}

// ScoreRisk maps a simple risk posture into a numeric score for later recommendation blending.
func (u *UseCase) ScoreRisk() (float64, error) {
	return 0.6, nil
}

// BuildRecommendation synthesizes a recommendation object from the incoming technical score.
// The 0.75/0.4 thresholds are asymmetric on purpose: a mediocre score should HOLD rather than
// SELL, since exiting a position is a stronger claim than staying put. Both are placeholder
// heuristics pending real threshold tuning (see orchestrator.go's blend weighting).
func (u *UseCase) BuildRecommendation(input decision.TechnicalScore) (decision.Recommendation, error) {
	if input.Overall >= 0.75 {
		return decision.Recommendation{
			Symbol:     "",
			Action:     "BUY",
			Confidence: 0.78,
			Reason:     []string{"Momentum signal is favorable", "Decision-support layer only"},
		}, nil
	}
	if input.Overall <= 0.4 {
		return decision.Recommendation{
			Symbol:     "",
			Action:     "SELL",
			Confidence: 0.72,
			Reason:     []string{"Momentum signal is weak", "Decision-support layer only"},
		}, nil
	}
	return decision.Recommendation{
		Symbol:     "",
		Action:     "HOLD",
		Confidence: 0.6,
		Reason:     []string{"Mixed signal composition", "Decision-support layer only"},
	}, nil
}

// Recommend produces a decision-support recommendation with rationale.
func (u *UseCase) Recommend(symbol string) (decision.Recommendation, error) {
	recommendation := decision.Recommendation{
		Symbol:     symbol,
		Action:     "HOLD",
		Confidence: 0.6,
		Reason:     []string{"Decision-support layer only", "No automated trading action"},
	}
	return recommendation, nil
}
