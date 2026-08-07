package ml

// StubModelVersion tags predictions from the deterministic RSI-derived fallback (no model
// loaded — see application/ml.UseCase.Predict). Referenced by persistence to decide whether
// CorrectionProbability is a real model output or just its unset zero value (see
// infrastructure/persistence/ml.go's SavePrediction).
const StubModelVersion = "stub-v0"

type FeatureVector struct {
	RSI    float64 `json:"rsi"`
	EMA    float64 `json:"ema"`
	MACD   float64 `json:"macd"`
	ATR    float64 `json:"atr"`
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
	// correction-probability one), 0 otherwise. Trained on a genuinely small, symbol-skewed
	// first dataset (222 rows, 3 of 8 symbols with zero recovered examples as of this model's
	// training — see docs/ml.md's Implementation status note) — honest, not fabricated, but
	// worth reading with real caution until more history accumulates, same stance this project
	// takes on CorrectionProbability's own first model.
	RecoveryProbability float64 `json:"recovery_probability"`

	ModelVersion string `json:"model_version"`

	// RecoveryModelVersion is set only when a recovery model actually ran (see
	// application/ml.UseCase.Predict), independent of ModelVersion above — mirrors
	// ModelVersion's role of letting persistence tell "not computed" (empty) apart from "computed
	// as exactly 0" (see infrastructure/persistence/ml.go's SavePrediction).
	RecoveryModelVersion string `json:"recovery_model_version,omitempty"`
}
