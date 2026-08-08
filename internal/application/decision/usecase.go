package decision

import (
	"math"

	"atlas/internal/domain/decision"
)

// UseCase contains the first deterministic scoring rules for recommendation generation.
// Thresholds defaults to decision.DefaultThresholds() (the original hand-picked cutoffs used
// everywhere in live traffic) but is a plain field, not a package constant, so
// backtesting.UseCase can construct a UseCase with different candidate thresholds per
// simulation run without ever mutating the live instance — see NewUseCaseWithThresholds.
type UseCase struct {
	Thresholds decision.Thresholds
}

func NewUseCase() *UseCase {
	return &UseCase{Thresholds: decision.DefaultThresholds()}
}

// NewUseCaseWithThresholds constructs a UseCase around an explicit, caller-supplied threshold
// set instead of the defaults — used by backtesting's walk-forward optimizer to evaluate
// candidate thresholds against historical data without touching the live decision engine.
func NewUseCaseWithThresholds(thresholds decision.Thresholds) *UseCase {
	return &UseCase{Thresholds: thresholds}
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

// scoreConfidence maps how far the blended score sits from the neutral center (0.5) into a
// [0,1] confidence value — the same idea already used for the ML sub-model's own confidence
// (see ml.UseCase.Predict's Confidence field), applied here to the engine's final blended score
// so Recommendation.Confidence reflects how decisive the actual signal was instead of a fixed
// literal per action band (the previous behavior: every BUY reported 0.78, every HOLD reported
// 0.6, regardless of how strong or borderline the underlying score actually was).
func scoreConfidence(overall float64) float64 {
	return clamp01(math.Abs(overall-0.5) * 2)
}

// BuildRecommendation synthesizes a recommendation from the blended technical score, real
// portfolio position state, and ML confidence — see docs/decision_engine.md's "Recommendation
// Types". BUY/SELL are gated on NOT already holding vs. holding a position; ACCUMULATE/REDUCE
// are their position-aware counterparts (adding to a position already held is a different claim
// than opening a new one; trimming a position is a different claim than exiting it entirely).
// WAIT vs HOLD is gated on MLConfidence: a genuinely mixed score (near 0.5) is HOLD outright,
// but a score that leans one direction without enough model confidence behind it is WAIT — a
// real signal, just not a trustworthy one yet — rather than being silently collapsed into HOLD.
func (u *UseCase) BuildRecommendation(input decision.RecommendationInput) (decision.Recommendation, error) {
	overall := input.Technical.Overall
	confidence := scoreConfidence(overall)

	if overall >= u.Thresholds.StrongBullish {
		if input.HasPosition {
			return decision.Recommendation{
				Action:     "ACCUMULATE",
				Confidence: confidence,
				Reason:     []string{"Momentum signal is favorable", "Adding to an existing position", "Decision-support layer only"},
			}, nil
		}
		return decision.Recommendation{
			Action:     "BUY",
			Confidence: confidence,
			Reason:     []string{"Momentum signal is favorable", "Decision-support layer only"},
		}, nil
	}

	if overall <= u.Thresholds.StrongBearish {
		if input.HasPosition {
			return decision.Recommendation{
				Action:     "SELL",
				Confidence: confidence,
				Reason:     []string{"Momentum signal is weak", "Decision-support layer only"},
			}, nil
		}
		return u.waitOrHold(overall, input.MLConfidence, "Momentum signal is weak, but no position exists to act on"), nil
	}

	if overall <= u.Thresholds.LeanBearish {
		if input.HasPosition {
			return decision.Recommendation{
				Action:     "REDUCE",
				Confidence: confidence,
				Reason:     []string{"Momentum signal is softening", "Trimming an existing position rather than exiting it", "Decision-support layer only"},
			}, nil
		}
		return u.waitOrHold(overall, input.MLConfidence, "Momentum signal is softening, but no position exists to act on"), nil
	}

	if overall >= u.Thresholds.LeanBullish {
		return u.waitOrHold(overall, input.MLConfidence, "Momentum signal leans favorable but isn't strong enough yet"), nil
	}

	return decision.Recommendation{
		Action:     "HOLD",
		Confidence: confidence,
		Reason:     []string{"Mixed signal composition", "Decision-support layer only"},
	}, nil
}

// waitOrHold resolves WAIT vs HOLD for a score that leans one way without crossing a
// strong-enough threshold to act (see BuildRecommendation's band structure). WAIT's own
// Confidence field reports mlConfidence as-is, not scoreConfidence(overall): WAIT's whole point
// is "here's a lean, but the model isn't sure" — reporting a higher confidence than the model
// actually has would contradict the recommendation being made. HOLD, when reached from here,
// uses scoreConfidence like every other action — a lean near the WAIT/HOLD boundary is
// genuinely less decisive than a strong one, and should report that honestly.
func (u *UseCase) waitOrHold(overall, mlConfidence float64, leadReason string) decision.Recommendation {
	if mlConfidence < u.Thresholds.LowMLConfidence {
		return decision.Recommendation{
			Action:     "WAIT",
			Confidence: mlConfidence,
			Reason:     []string{leadReason, "ML confidence too low to act on this signal yet", "Decision-support layer only"},
		}
	}
	return decision.Recommendation{
		Action:     "HOLD",
		Confidence: scoreConfidence(overall),
		Reason:     []string{"Mixed signal composition", "Decision-support layer only"},
	}
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
