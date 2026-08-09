# ML

- Two independent, optional binary classifiers exist: correction-probability (`MODEL_PATH`) and
  recovery-probability (`RECOVERY_MODEL_PATH`) — same XGBoost→ONNX pipeline, same Go inference
  mechanics (`ml.OnnxModel.PredictCorrectionProbability` is reused for both roles; only the label
  they were trained against differs). Either, both, or neither can be loaded; nothing else in the
  codebase assumes one implies the other.
- `internal/application/ml.FeatureOrder` (Go) and `training/train.py`'s `FEATURE_ORDER` (Python)
  **must stay in the same order** — `rsi, ema_dist, macd_pct, tr_pct, atr_pct, rvol_20, rvol_50, volume` today. The ONNX
  model has no column names at inference time, only positional floats; `ml.LoadOnnxModel` refuses
  to load a model whose metadata sidecar's `feature_order` doesn't match, so a mismatch fails
  loudly at load time instead of silently mis-scoring — don't remove that check to "just make it
  load."
- **Every feature must be scale-free** — a ratio or a bounded index, never a raw price or share
  count. This is the one hard rule in this file. The original set (`ema`, `macd`, `atr` in price
  units) let a single feature encode the symbol's price level, and since per-symbol correction
  base rates run from 0.00 to 0.58, the model scored by memorising symbol identity: a predictor
  using no features at all beat it, 0.757 AUC to 0.707. If you add a feature, normalise it by
  close (or bound it) in `ml.BuildFeatureVector`, and rename it if you changed an existing one's
  meaning — the `feature_order` guard compares names, so reusing a name silently loads stale
  models against new semantics.
- `cmd/export-training-data` reuses the exact same Go feature-computation code
  (`ml.BuildFeatureVector`) the live inference path uses — normalisation included, which is why
  it lives there and not in the exporter. Never reimplement indicator math in Python; that's
  exactly the drift this design avoids.
- `training/train.py` computes a **symbol-base-rate baseline** ("predict each symbol's historical
  positive rate, ignore all features") on every run and warns loudly when the model loses to it.
  Don't delete that check to make a run look clean — it exists because a model losing to it once
  shipped as production-looking numbers.
- The verdict is judged on **PR AUC, not ROC AUC**, and that is load-bearing rather than pedantic.
  Atlas alerts on the top of the ranking; ROC AUC is dominated by the uneventful middle no alert
  touches. They disagree in practice: the shipped correction model trails the baseline on ROC AUC
  (0.710 vs 0.748) while beating it on PR AUC (0.264 vs 0.188), and at its 25 highest-scoring bars
  it was right 68% of the time against a 6.5% base rate. Judging on ROC AUC would condemn a model
  that does the job it is deployed to do.
- **Volatility is the dominant feature family for this target** — the largest single win in the
  project's history. `atr_pct` alone took prototype ROC AUC from 0.675 to 0.782; adding
  `rvol_20`/`rvol_50` roughly doubled PR AUC. Note `analyzer.ComputeATR` returns raw per-bar true
  range despite its name; `ComputeSmoothedATR` is the real ATR and the one the model uses. Longer
  windows (100+ bars) were tried and lose — most symbols hold only ~125 bars.
- Don't add `scale_pos_weight` back. Measured on this dataset it bought ~0.01 ROC AUC and
  tripled the Brier score (0.211 vs 0.067). The output is consumed as a probability, not a
  ranking — the decision engine blends `1 - correction_probability` — so calibration wins.
- Every trained model ships with a `.json` metadata sidecar next to its `.onnx` file (version,
  training row count, holdout ROC AUC, PR AUC, Brier score, accuracy, the symbol-base-rate
  baseline and whether the model beat it, label distribution, label definition,
  `feature_order`) — required, not optional documentation. `ml.ListModels` (the Account
  dashboard's model picker) reads it to build each option's description from real numbers, never
  a fabricated or hand-edited one.
- Recovery labels are conditional: `ml.ComputeRecoveryLabel` only produces a label for bars where
  a correction actually happened (`ComputeCorrectionLabel` returned 1) — recovery isn't a
  meaningful question otherwise. This makes the recovery training set structurally much smaller
  than the correction set. Note that "smaller" turned out not to mean "worse": once the features
  were made scale-free, recovery was the model that beat its baseline and correction was the one
  that didn't, reversing this project's earlier assumption. Judge each against its own baseline
  rather than by dataset size.
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