package ml

// StubModelVersion tags predictions from the deterministic RSI-derived fallback (no model
// loaded — see application/ml.UseCase.Predict). Referenced by persistence to decide whether
// CorrectionProbability is a real model output or just its unset zero value (see
// infrastructure/persistence/ml.go's SavePrediction).
const StubModelVersion = "stub-v0"

// FeatureVector is the ML model's input, and every field in it is deliberately **scale-free** —
// a ratio or a bounded index, never a raw price or share count. This is not a style preference;
// it was a real modelling bug once. The original version fed the analyzer's raw EMA/MACD/ATR
// straight in, which meant a single feature carried the symbol's absolute price level: EMA
// ranged from ~9 (PGE.WA) to ~21,957 (LPP.WA) across the collected universe, so any threshold
// split on it was an exact symbol lookup. Since per-symbol correction base rates range from 0.00
// (GLD, SPY, QQQ, IAU, IBIT, GLDM) to 0.58 (SOXL), the model could score well by memorising
// "which symbol is this" while learning nothing about price dynamics — and measurably did: a
// predictor using *no features at all*, only each symbol's historical correction rate, beat the
// trained model on the same holdout (AUC 0.757 vs 0.707). Normalising by the latest close fixed
// the input side of that; training/train.py's symbol-base-rate baseline keeps it honest going
// forward by refusing to let a model that can't beat that baseline look good.
//
// Field names are load-bearing: application/ml.FeatureOrder and training/train.py's
// FEATURE_ORDER must list them in this exact order, and ml.LoadOnnxModel refuses to load a model
// whose metadata sidecar disagrees. The rename away from ema/macd/atr was itself part of the fix
// — keeping the old names for newly-normalised values would have let a pre-normalisation model
// load cleanly and silently mis-score, since the guard compares names, not meanings.
type FeatureVector struct {
	// RSI is the 14-period Wilder RSI, already bounded to 0-100 and scale-free as-is. Falls
	// back to a neutral 50 before the indicator's warm-up (see application/ml.BuildFeatureVector).
	RSI float64 `json:"rsi"`

	// EMADist is how far the latest close sits from its 14-period EMA, as a fraction of the EMA
	// ((close-ema)/ema) — "3% above its average" instead of "trading at 294".
	EMADist float64 `json:"ema_dist"`

	// MACDPct is the MACD line as a fraction of the latest close (macd/close). MACD is a
	// difference of two EMAs, so its raw magnitude scales directly with the price level.
	MACDPct float64 `json:"macd_pct"`

	// TRPct is the latest bar's true range as a fraction of the latest close (tr/close) — a
	// single-bar volatility reading. Named for what analyzer.ComputeATR actually returns, which
	// is the per-bar true range rather than an average of it despite that method's name.
	TRPct float64 `json:"tr_pct"`

	// ATRPct is the 14-period Wilder-smoothed ATR as a fraction of the latest close — the same
	// quantity as TRPct but stable instead of one-bar noisy, and the single most valuable
	// feature in this set. Adding it lifted holdout ROC AUC from 0.675 to 0.782 on real data,
	// far more than any other change tried.
	//
	// This is not a coincidence: volatility is what separates instruments that correct often
	// from ones that never do (SOXL corrected in 58% of windows, GLD in 0%), and expressing
	// that as a real, measurable feature is what lets the model rank a symbol it has never seen
	// — see the FeatureVector doc comment above for why that distinction is the whole point.
	ATRPct float64 `json:"atr_pct"`

	// RealizedVol20 and RealizedVol50 are the sample standard deviation of close-to-close
	// returns over the trailing 20 and 50 bars. They complement ATRPct rather than repeating
	// it: ATR measures intrabar range including gaps and wicks, realized volatility measures
	// dispersion of closes, and the two disagree precisely when a symbol gaps or whipsaws.
	// Together with ATRPct they roughly doubled holdout PR AUC (0.222 to 0.392).
	//
	// Longer windows were tried and rejected on evidence, not taste: most symbols here hold
	// only ~125 bars, so a 100-bar rolling window discarded a third of the dataset and made
	// every metric worse.
	RealizedVol20 float64 `json:"rvol_20"`
	RealizedVol50 float64 `json:"rvol_50"`

	// Volume is the bar's volume relative to the mean volume of the bars supplied
	// (analyzer.ComputeVolume) — already a ratio, so it needed no rescaling. Note the averaging
	// window still differs between training (expanding: every bar up to i) and live inference
	// (the ~COLLECTOR_WINDOW_DAYS collection window), a known remaining train/serve skew.
	Volume float64 `json:"volume"`
}

type Prediction struct {
	Symbol     string  `json:"symbol"`
	Confidence float64 `json:"confidence"`

	// Score is a bullish-lean signal in [0,1] for the decision engine's blend (see
	// decision.Orchestrator.blendMLAndPortfolio) — higher means more supportive of buying.
	// When a real correction-probability model is loaded, Score is derived as
	// 1-CorrectionProbability so existing blend semantics don't change; the deterministic
	// stub still derives it directly from RSI when no model is loaded.
	Score float64 `json:"score"`

	// CorrectionProbability is the model's raw output — the probability of a price correction
	// within the training label's horizon (see application/ml.CorrectionLabelParams). Zero
	// when no trained model is loaded (the RSI-derived stub has no such concept).
	CorrectionProbability float64 `json:"correction_probability"`

	// RecoveryProbability is the probability that a price correction recovers to its pre-drop
	// level within application/ml.RecoveryLabelParams' horizon (see
	// application/ml.ComputeRecoveryLabel for the label definition). Real when a recovery model
	// is loaded (RECOVERY_MODEL_PATH — a separate, optional model from ModelPath/MODEL_PATH's
	// correction-probability one), 0 otherwise. Always the thinner and more skewed of the two
	// models by construction, since a recovery label only exists for bars where a correction
	// actually happened — treat its metrics with more scepticism than the correction model's,
	// not the same amount. See docs/ml.md's Implementation status note for the current numbers.
	RecoveryProbability float64 `json:"recovery_probability"`

	ModelVersion string `json:"model_version"`

	// RecoveryModelVersion is set only when a recovery model actually ran (see
	// application/ml.UseCase.Predict), independent of ModelVersion above — mirrors
	// ModelVersion's role of letting persistence tell "not computed" (empty) apart from "computed
	// as exactly 0" (see infrastructure/persistence/ml.go's SavePrediction).
	RecoveryModelVersion string `json:"recovery_model_version,omitempty"`
}
