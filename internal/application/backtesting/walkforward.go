package backtesting

import (
	"fmt"
	"math"

	domainDecision "atlas/internal/domain/decision"
	"atlas/internal/domain/market"

	"atlas/internal/domain/backtesting"
)

// Walk-forward split ratios per docs/backtesting.md's "Walk Forward Testing"/"Data Splitting"
// sections: proportional, chronological, never shuffled. The remaining ~20% after train+validate
// becomes the test split. Fixed at 60/20/20 rather than caller-supplied date ranges — a
// deliberate simplification agreed on with the Author to keep the API simple (no date-range
// parameters); see implementation-plan.md's Phase 6 status note.
const (
	trainRatio    = 0.6
	validateRatio = 0.2
)

// thresholdSearchDeltas are the offsets tried around decision.DefaultThresholds() for the four
// score-band thresholds during optimization — a small, fixed grid (not user-configurable), kept
// deliberately narrow so the search stays fast and close to the hand-picked defaults rather
// than wandering into untested territory.
var thresholdSearchDeltas = []float64{-0.05, 0, 0.05}

// lowMLConfidenceSearchDeltas is the equivalent grid for LowMLConfidence, given a wider natural
// range (WAIT vs. HOLD is a coarser distinction than the four score bands).
var lowMLConfidenceSearchDeltas = []float64{-0.10, 0, 0.10}

// RunWalkForward implements docs/backtesting.md's "Walk Forward Testing" and "Optimization"
// sections: bars are split chronologically into train/validate/test (splitWalkForward), a small
// grid search over decision.Thresholds variations is evaluated against the train split only
// (optimizeThresholds), and the winning thresholds are then run once, unmodified, against
// validate and test. Nothing here ever touches u.Orchestrator.Decision — the result is a report
// for a human to review (BestThresholds), never an automatic change to the live decision engine.
func (u *UseCase) RunWalkForward(symbol string, bars []market.Bar, initialCapital float64, strategyName, strategyVersion string) (backtesting.WalkForwardResult, error) {
	if err := u.validateInputs(strategyName, initialCapital); err != nil {
		return backtesting.WalkForwardResult{}, err
	}

	trainBars, validateBars, testBars, err := splitWalkForward(bars)
	if err != nil {
		return backtesting.WalkForwardResult{}, err
	}

	bestThresholds, candidatesEvaluated, err := u.optimizeThresholds(symbol, trainBars, initialCapital, strategyName, strategyVersion)
	if err != nil {
		return backtesting.WalkForwardResult{}, fmt.Errorf("optimize thresholds on train split: %w", err)
	}

	trainResult, err := u.simulate(symbol, trainBars, initialCapital, strategyName, strategyVersion, bestThresholds)
	if err != nil {
		return backtesting.WalkForwardResult{}, fmt.Errorf("simulate train split: %w", err)
	}
	validateResult, err := u.simulate(symbol, validateBars, initialCapital, strategyName, strategyVersion, bestThresholds)
	if err != nil {
		return backtesting.WalkForwardResult{}, fmt.Errorf("simulate validate split: %w", err)
	}
	testResult, err := u.simulate(symbol, testBars, initialCapital, strategyName, strategyVersion, bestThresholds)
	if err != nil {
		return backtesting.WalkForwardResult{}, fmt.Errorf("simulate test split: %w", err)
	}

	return backtesting.WalkForwardResult{
		Symbol:              symbol,
		BestThresholds:      bestThresholds,
		CandidatesEvaluated: candidatesEvaluated,
		Train:               trainResult,
		Validate:            validateResult,
		Test:                testResult,
	}, nil
}

// splitWalkForward divides bars chronologically into train/validate/test (trainRatio,
// validateRatio, remainder), per docs/backtesting.md's "never randomly shuffle financial time
// series" rule — each split is a contiguous, ordered slice, and no split ever overlaps or sees
// another's future. Each split must independently exceed minLookback (it gets its own fresh
// indicator warm-up and simulated portfolio in simulate, exactly like a standalone Run) —
// callers should request a wide enough window (roughly 5x minLookback or more) for all three
// splits to be usable.
func splitWalkForward(bars []market.Bar) (train, validate, test []market.Bar, err error) {
	trainEnd := int(float64(len(bars)) * trainRatio)
	validateEnd := trainEnd + int(float64(len(bars))*validateRatio)

	splits := []struct {
		name string
		bars []market.Bar
	}{
		{"train", bars[:trainEnd]},
		{"validate", bars[trainEnd:validateEnd]},
		{"test", bars[validateEnd:]},
	}
	for _, s := range splits {
		if len(s.bars) <= minLookback {
			return nil, nil, nil, fmt.Errorf(
				"not enough bars for the %s split (%d, need more than %d) — request a wider window for walk-forward",
				s.name, len(s.bars), minLookback)
		}
	}
	return splits[0].bars, splits[1].bars, splits[2].bars, nil
}

// optimizeThresholds performs a small grid search around decision.DefaultThresholds() (see
// thresholdSearchDeltas/lowMLConfidenceSearchDeltas), evaluating each valid candidate (see
// domainDecision.Thresholds.Valid) by simulating it against bars and ranking by Sharpe Ratio —
// a standard risk-adjusted return metric, chosen over raw Total Return specifically so the
// search doesn't reward high-variance blowups (per docs/backtesting.md's "Optimization" section
// asking results to be compared on Return/Risk/Drawdown together, not return alone). Returns the
// best candidate found and how many valid combinations were actually evaluated (for
// transparency in the report — see WalkForwardResult.CandidatesEvaluated).
func (u *UseCase) optimizeThresholds(symbol string, bars []market.Bar, initialCapital float64, strategyName, strategyVersion string) (domainDecision.Thresholds, int, error) {
	base := domainDecision.DefaultThresholds()
	best := base
	bestSharpe := math.Inf(-1)
	evaluated := 0

	for _, dStrongBullish := range thresholdSearchDeltas {
		for _, dLeanBullish := range thresholdSearchDeltas {
			for _, dLeanBearish := range thresholdSearchDeltas {
				for _, dStrongBearish := range thresholdSearchDeltas {
					for _, dLowMLConfidence := range lowMLConfidenceSearchDeltas {
						candidate := domainDecision.Thresholds{
							StrongBullish:   base.StrongBullish + dStrongBullish,
							LeanBullish:     base.LeanBullish + dLeanBullish,
							LeanBearish:     base.LeanBearish + dLeanBearish,
							StrongBearish:   base.StrongBearish + dStrongBearish,
							LowMLConfidence: base.LowMLConfidence + dLowMLConfidence,
						}
						if !candidate.Valid() {
							continue
						}

						result, err := u.simulate(symbol, bars, initialCapital, strategyName, strategyVersion, candidate)
						if err != nil {
							return domainDecision.Thresholds{}, evaluated, err
						}
						evaluated++
						if result.SharpeRatio > bestSharpe {
							bestSharpe = result.SharpeRatio
							best = candidate
						}
					}
				}
			}
		}
	}

	return best, evaluated, nil
}
