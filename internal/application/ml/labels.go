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
	threshold := base * (1 - params.CorrectionParams.ThresholdPct)
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
