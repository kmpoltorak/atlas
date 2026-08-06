package ml

import "atlas/internal/domain/market"

// MinFeatureLookback is the minimum number of bars visible before a feature vector is built
// for training, matching analyzer.ComputeRSI's 15-bar warm-up (see backtesting.minLookback for
// the same threshold used at inference time) — rows before this would carry a neutral RSI
// fallback rather than a real computed value, which would teach the model a value that never
// occurs during live inference on a warmed-up symbol.
const MinFeatureLookback = 15

// CorrectionLabelParams configures how a historical training label is derived from forward
// price action, per docs/ml.md's "correction probability" target design.
type CorrectionLabelParams struct {
	HorizonBars  int     // how many bars ahead to look for a correction
	ThresholdPct float64 // drop fraction (e.g. 0.05 = 5%) that counts as a correction
}

// DefaultCorrectionLabelParams is a first-pass, documented choice — a 5% drop within the next
// 10 bars — not a tuned threshold. It's recorded in training/train.py's metadata sidecar
// alongside every exported model so the label definition a given model was trained against is
// always traceable.
func DefaultCorrectionLabelParams() CorrectionLabelParams {
	return CorrectionLabelParams{HorizonBars: 10, ThresholdPct: 0.05}
}

// ComputeCorrectionLabel looks forward from bars[i] and reports whether any subsequent close
// within params.HorizonBars drops more than params.ThresholdPct below bars[i].Close. ok is
// false when there isn't enough forward history to evaluate the label (bars[i] is within
// HorizonBars of the end of the series) — those rows must be excluded from training entirely,
// not labeled 0, since "no correction observed" and "not enough data to know" are different
// claims and conflating them would bias the model toward "no correction."
func ComputeCorrectionLabel(bars []market.Bar, i int, params CorrectionLabelParams) (label int, ok bool) {
	if i < 0 || i >= len(bars) {
		return 0, false
	}
	end := i + params.HorizonBars
	if end >= len(bars) {
		return 0, false
	}
	base := bars[i].Close
	if base <= 0 {
		return 0, false
	}
	threshold := base * (1 - params.ThresholdPct)
	for _, bar := range bars[i+1 : end+1] {
		if bar.Close <= threshold {
			return 1, true
		}
	}
	return 0, true
}
