package ml

import (
	"math"

	"atlas/internal/domain/market"
)

// MinFeatureLookback is the minimum number of bars visible before a feature vector is built
// for training, matching analyzer.ComputeRSI's 15-bar warm-up (see backtesting.minLookback for
// the same threshold used at inference time) — rows before this would carry a neutral RSI
// fallback rather than a real computed value, which would teach the model a value that never
// occurs during live inference on a warmed-up symbol.
const MinFeatureLookback = 15

// CorrectionLabelParams configures how a historical training label is derived from forward
// price action, per docs/ml.md's "correction probability" target design.
//
// The threshold can be either a fixed fraction of price or a multiple of the instrument's own
// realised volatility, and which one is used matters more than it looks. A fixed 5% asks
// different questions of different instruments: over the collected universe GLD never once fell
// that far in ten bars while SOXL did so in 58% of windows, so the label ends up encoding "how
// volatile is this ticker" rather than "is a correction coming". That is precisely why a
// predictor using no features at all — just each symbol's historical rate — was so hard to beat.
// Scaling the barrier by volatility is the standard remedy (the triple-barrier method's
// volatility-scaled barriers), and it makes the question comparable across instruments: "an
// unusually large drop *for this instrument*".
type CorrectionLabelParams struct {
	HorizonBars int // how many bars ahead to look for a correction

	// ThresholdPct is a fixed drop fraction (0.05 = 5%). Used when VolatilityMultiple is 0.
	ThresholdPct float64

	// VolatilityMultiple, when > 0, replaces ThresholdPct: the barrier becomes this multiple of
	// the trailing realised volatility measured over VolatilityWindow bars, computed strictly
	// from bars at or before the feature bar so no forward information leaks into the label.
	VolatilityMultiple float64

	// VolatilityWindow is how many trailing bars the realised volatility is measured over. Only
	// consulted when VolatilityMultiple > 0.
	VolatilityWindow int

	// MinThresholdPct floors the volatility-scaled barrier so that an unusually quiet stretch
	// cannot produce a near-zero threshold that labels ordinary noise as a correction. Only
	// consulted when VolatilityMultiple > 0.
	MinThresholdPct float64
}

// DefaultCorrectionLabelParams: a drop of 4x the instrument's own trailing 20-bar volatility
// within the next 10 bars, floored at 2% so a becalmed instrument can't turn ordinary noise
// into a "correction".
//
// The 4x multiple was measured, not guessed, against the collected universe (47 symbols of real
// hourly bars). It was chosen on two criteria at once: it keeps the overall positive rate close
// to the fixed-5% rule it replaces, so the target stays recognisably the same question, and it
// spreads those positives far more evenly across instruments, which is the entire point.
//
//	rule          overall   spread across symbols   symbols never triggering   symbols over 40%
//	fixed 5%       0.110            0.168                     12                      6
//	vol x3.0       0.185            0.102                      1                      1
//	vol x4.0       0.132            0.081                      1                      0
//
// Under the fixed rule a quarter of the universe could not produce a single positive example,
// while a handful triggered in over 40% of windows — so "will this correct" was really asking
// "is this a volatile ticker", and a lookup table of per-symbol rates was accordingly hard to
// beat. Lower multiples were rejected for drifting too far above the original positive rate.
//
// Every exported model records the exact parameters it was trained against in its metadata
// sidecar (see training/train.py), so a model can always be traced back to its label definition.
func DefaultCorrectionLabelParams() CorrectionLabelParams {
	return CorrectionLabelParams{
		HorizonBars:        10,
		ThresholdPct:       0.05,
		VolatilityMultiple: 4.0,
		VolatilityWindow:   20,
		MinThresholdPct:    0.02,
	}
}

// thresholdFraction resolves the drop fraction that counts as a correction at bars[i]. With
// VolatilityMultiple unset this is simply ThresholdPct; otherwise it is the trailing realised
// volatility at bars[i] scaled by VolatilityMultiple and floored at MinThresholdPct.
//
// The volatility is measured over bars[:i+1] only. That constraint is the whole reason this is
// computed here per-bar rather than once per symbol: a threshold derived from the full series
// would embed information from after the feature bar, and the label would quietly become
// unlearnable-in-production.
func (p CorrectionLabelParams) thresholdFraction(bars []market.Bar, i int) float64 {
	if p.VolatilityMultiple <= 0 {
		return p.ThresholdPct
	}
	window := p.VolatilityWindow
	if window < 2 {
		window = 20
	}
	start := i - window + 1
	if start < 1 {
		start = 1
	}
	if i-start < 1 {
		return p.MinThresholdPct
	}

	returns := make([]float64, 0, i-start+1)
	for j := start; j <= i; j++ {
		if prev := bars[j-1].Close; prev > 0 {
			returns = append(returns, bars[j].Close/prev-1)
		}
	}
	if len(returns) < 2 {
		return p.MinThresholdPct
	}
	mean := 0.0
	for _, r := range returns {
		mean += r
	}
	mean /= float64(len(returns))
	sumSq := 0.0
	for _, r := range returns {
		sumSq += (r - mean) * (r - mean)
	}
	sigma := math.Sqrt(sumSq / float64(len(returns)-1))

	threshold := p.VolatilityMultiple * sigma
	if threshold < p.MinThresholdPct {
		return p.MinThresholdPct
	}
	return threshold
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
	threshold := base * (1 - params.thresholdFraction(bars, i))
	for _, bar := range bars[i+1 : end+1] {
		if bar.Close <= threshold {
			return 1, true
		}
	}
	return 0, true
}

// RecoveryLabelParams configures how a historical training label is derived for "did a
// correction recover" — see docs/ml.md's "recovery probability" target design. CorrectionParams
// reuses the same correction definition ComputeCorrectionLabel already uses, since recovery is
// only a meaningful question once a correction has actually happened.
type RecoveryLabelParams struct {
	CorrectionParams    CorrectionLabelParams
	RecoveryHorizonBars int // how many bars after the correction breach to look for a recovery
}

// DefaultRecoveryLabelParams is a first-pass, documented choice — a 20-bar recovery window
// after DefaultCorrectionLabelParams' correction — not a tuned threshold, same honesty stance as
// DefaultCorrectionLabelParams.
func DefaultRecoveryLabelParams() RecoveryLabelParams {
	return RecoveryLabelParams{CorrectionParams: DefaultCorrectionLabelParams(), RecoveryHorizonBars: 20}
}

// ComputeRecoveryLabel reports whether a correction that started at bars[i] (per
// params.CorrectionParams) recovered back to bars[i].Close (the pre-drop price) within
// params.RecoveryHorizonBars bars after the correction threshold was first breached. ok is
// false when no correction actually occurred at bars[i] at all (recovery isn't a meaningful
// question without a correction to recover from — see ComputeCorrectionLabel) or when there
// isn't enough forward history past the breach to know whether it recovered.
//
// cmd/export-training-data calls this behind -label-type=recovery, writing only the bars it
// returns ok for — see ml.Prediction.RecoveryProbability's doc comment and docs/ml.md's
// Implementation status note for why that makes the recovery dataset structurally smaller and
// more skewed than the correction one.
func ComputeRecoveryLabel(bars []market.Bar, i int, params RecoveryLabelParams) (label int, ok bool) {
	correctionLabel, correctionOK := ComputeCorrectionLabel(bars, i, params.CorrectionParams)
	if !correctionOK || correctionLabel == 0 {
		return 0, false
	}

	base := bars[i].Close
	threshold := base * (1 - params.CorrectionParams.thresholdFraction(bars, i))
	correctionEnd := i + params.CorrectionParams.HorizonBars

	breachIndex := -1
	for j := i + 1; j <= correctionEnd && j < len(bars); j++ {
		if bars[j].Close <= threshold {
			breachIndex = j
			break
		}
	}
	if breachIndex == -1 {
		// Unreachable given correctionLabel == 1 above, but guarded rather than assumed.
		return 0, false
	}

	recoveryEnd := breachIndex + params.RecoveryHorizonBars
	if recoveryEnd >= len(bars) {
		return 0, false
	}
	for _, bar := range bars[breachIndex+1 : recoveryEnd+1] {
		if bar.Close >= base {
			return 1, true
		}
	}
	return 0, true
}
