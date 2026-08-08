# ML

- Two independent, optional binary classifiers exist: correction-probability (`MODEL_PATH`) and
  recovery-probability (`RECOVERY_MODEL_PATH`) — same XGBoost→ONNX pipeline, same Go inference
  mechanics (`ml.OnnxModel.PredictCorrectionProbability` is reused for both roles; only the label
  they were trained against differs). Either, both, or neither can be loaded; nothing else in the
  codebase assumes one implies the other.
- `internal/application/ml.FeatureOrder` (Go) and `training/train.py`'s `FEATURE_ORDER` (Python)
  **must stay in the same order** — `rsi, ema, macd, atr, volume` today. The ONNX model has no
  column names at inference time, only positional floats; `ml.LoadOnnxModel` refuses to load a
  model whose metadata sidecar's `feature_order` doesn't match, so a mismatch fails loudly at
  load time instead of silently mis-scoring — don't remove that check to "just make it load."
- `cmd/export-training-data` reuses the exact same Go feature-computation code
  (`ml.BuildFeatureVector`) the live inference path uses. Never reimplement RSI/EMA/MACD/ATR/
  Volume math in Python — that's exactly the drift this design avoids.
- Every trained model ships with a `.json` metadata sidecar next to its `.onnx` file (version,
  training row count, holdout AUC/accuracy, label distribution, label definition,
  `feature_order`) — required, not optional documentation. `ml.ListModels` (the Account
  dashboard's model picker) reads it to build each option's description from real numbers, never
  a fabricated or hand-edited one.
- Recovery labels are conditional: `ml.ComputeRecoveryLabel` only produces a label for bars where
  a correction actually happened (`ComputeCorrectionLabel` returned 1) — recovery isn't a
  meaningful question otherwise. This means the recovery training set is always much smaller and
  more skewed than the correction set; treat any recovery model's metrics with more skepticism
  than the correction model's, not the same amount.
- Model deployment is never fully automatic. `training/retrain.sh` can generate a new candidate
  on a schedule, but it never changes what's live — a human picks a candidate from the Account
  dashboard page after reviewing its real holdout metrics (`ml.UseCase.SetModel`/
  `SetRecoveryModel`, hot-swapped via `atomic.Pointer`, no restart needed, durably recorded in
  `ml_model_selection` — migrations/011). This is deliberate: training data here is real but
  still thin and skewed, and this project's stance is "never auto-promote an unreviewed model
  into live recommendations" — the same rule already applied to walk-forward threshold
  optimization (`docs/backtesting.md`).
- When no model is loaded, `Predict()` falls back to a deterministic RSI-derived stub
  (`ml.StubModelVersion`) for correction, and leaves `recovery_probability` at `0` for recovery —
  a normal, logged, non-fatal startup state, not an error. Persistence follows a
  NULL-means-not-computed convention: a stub's correction probability is never written as a
  literal value, since `0`/`0.5` would misrepresent "not computed" as a real prediction.