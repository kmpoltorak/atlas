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

	// RecoveryProbability would be the probability that a price correction recovers to its
	// pre-drop level within application/ml.RecoveryLabelParams' horizon (see
	// application/ml.ComputeRecoveryLabel — the label definition is real and tested). Always 0
	// today: no model produces this. Training one honestly needs real correction+recovery
	// events across enough symbols/history, which the currently-collected dataset (as of this
	// field's introduction: 1 symbol, a few hundred bars) doesn't come close to providing — see
	// docs/ml.md's Implementation status note. This field exists so the contract and label
	// definition are ready the moment there's enough real data to train against, matching how
	// CorrectionProbability itself started before training/train.py existed.
	RecoveryProbability float64 `json:"recovery_probability"`

	ModelVersion string `json:"model_version"`
}
