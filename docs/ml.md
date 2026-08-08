# ML Architecture

**Implementation status**: the Python training pipeline described below is real and
implemented (see training/README.md for the runbook) — XGBoost binary classifier predicting
"correction probability" (a >5% close-price drop within 10 forward bars by default, see
`application/ml.CorrectionLabelParams`), trained on real collected OHLCV history (no synthetic
data), exported to ONNX. Go production inference (`internal/application/ml`, `OnnxModel`) loads
that ONNX model via `github.com/yalue/onnxruntime_go` and runs real inference when `MODEL_PATH`
is set and loads successfully — verified live end-to-end (train → export → load → real HTTP
`/predictions` response → persisted `correction_probability` row, not NULL). When no model is
loaded (the default), Predict falls back to the same deterministic RSI-derived stub as before
(`ml.StubModelVersion`, "stub-v0", fixed 0.72 confidence) — this is a normal, non-fatal startup
state, not an error. Caveats, honestly: training data today is whatever Atlas has actually
collected (see PROJECT_CONTEXT.md), which may be a small first dataset; validation is a simple
per-symbol time-ordered holdout, not full walk-forward validation (a separate, larger
backtesting gap, now real — see docs/backtesting.md); and `expected_duration`/`expected_strength`
(the predictions table columns beyond `correction_probability`) still stay NULL — no model
produces those yet.

**Recovery probability**: real now, trained on real accumulated history — `ml.
ComputeRecoveryLabel`/`RecoveryLabelParams` (internal/application/ml/labels.go) defines "did a
correction recover to its pre-drop price within N bars of the breach," `cmd/
export-training-data -label-type=recovery` exports it (only rows where a correction actually
occurred get a label — recovery isn't a meaningful question otherwise), and `training/train.py
--target recovery` trains and exports it exactly like the correction model, via the same
procedure (only the input CSV, output filename prefix, and metadata's label definition differ).
Go inference loads it as a second, fully independent optional model
(`RECOVERY_MODEL_PATH`/`OnnxModel` — separate from `MODEL_PATH`'s correction model; either, both,
or neither can be loaded) — verified live end-to-end (train → export → load → real HTTP
`/predictions` response → persisted `recovery_probability`/`recovery_model_version` row, not
NULL — migrations/010).

Caveats, honestly, and more pointed than the correction model's above: the real accumulated
dataset as of this model's first training pass is 222 rows across 7 symbols — and because a
recovery label is only meaningful conditional on a correction having happened at all, those 222
rows are themselves a small, skewed subset of a modest correction history, not 222 independent
observations (many are overlapping rolling windows around the same handful of actual price
moves). 3 of those 7 symbols (AAPL, NVDA, ORCL) have **zero** recovered examples in the data at
all, meaning the model may be partly learning "which symbol is this" rather than genuine recovery
dynamics. First holdout: AUC 0.83, accuracy 0.65 on an n=46 holdout — a real number, not
fabricated, but one to read with real skepticism given the sample size and skew, not as a
production-grade signal yet. This is the same honesty stance as `docs/backtesting.md`'s ML
evaluation and the correction model's own first-pass caveats above — the number is printed and
persisted to the metadata sidecar precisely so a small/overconfident model stays visible, not
hidden.

**Market condition**: real and usable today, but *not* a trained model —
`ml.ClassifyMarketCondition` is a deterministic ADX+EMA technical-analysis rule (ADX below 20 =
SIDEWAYS regardless of direction; otherwise BULL/BEAR from EMA's latest direction), which is
well-established technical analysis that doesn't need labeled historical outcomes to define, so
it doesn't share recovery-probability's data-volume blocker. Wired into
`Recommendation.MarketCondition` (`GET /recommendations`, persisted in the `recommendations`
table — migrations/007) — see docs/decision_engine.md.

## Training

Framework:
Python ecosystem — pandas, numpy, scikit-learn, XGBoost, onnx, onnxmltools, onnxruntime (see
training/requirements.txt for pinned versions; several are pinned together deliberately after
hitting real conversion incompatibilities — see that file's comments).

Feature computation is **not** reimplemented in Python: `cmd/export-training-data` reuses the
exact same Go analyzer-backed `ml.BuildFeatureVector` the live inference path uses, so training
and serving can never drift apart on RSI/EMA/MACD/ATR/Volume math. Python only trains on the
resulting CSV and computes the label (also computed in Go, from real forward price action — see
`ml.ComputeCorrectionLabel`).

Optuna (hyperparameter search) is not implemented — the current pipeline uses fixed, reasonable
XGBoost hyperparameters, not a documented gap requiring a training-data-volume increase first.

## Model Lifecycle
1. Collect historical data — via Atlas's existing collector (`GET /prices`), not a separate
   ingestion path.
2. Generate features — `cmd/export-training-data` (Go, reuses live feature code).
3. Train model — `training/train.py` (XGBoost, time-ordered holdout).
4. Validate model — holdout AUC/accuracy + label-balance checks, printed and written to the
   model's metadata sidecar (`training/models/atlas-ml-<version>.json`).
5. Export model — ONNX (`onnxmltools.convert_xgboost`).
6. Deploy model — copy/mount the `.onnx` file (+ its `.json` metadata sidecar, required — see
   below) and point `MODEL_PATH` at it; not automated/CI-driven yet (see training/README.md).

Every exported model is versioned via its filename (`atlas-ml-<version>.onnx`) and a metadata
JSON sidecar recording feature order, training row count, holdout metrics, and the label
definition it was trained against — `internal/application/ml.LoadOnnxModel` refuses to load a
model whose sidecar `feature_order` doesn't match Go's expected order, so a mismatched/stale
model fails loudly at startup instead of silently mis-scoring.

## Model selection and retraining

Both models can be switched at runtime from the Account dashboard page (`/dashboard/account`),
not only via `MODEL_PATH`/`RECOVERY_MODEL_PATH` at startup: it lists every model
`ML_MODELS_DIR` has a metadata sidecar for (real holdout AUC/accuracy/row count, not fabricated),
and activating one hot-swaps it into live inference immediately (`ml.UseCase.SetModel`/
`SetRecoveryModel`, an `atomic.Pointer` swap — safe under concurrent requests) with no restart.
The choice is durably recorded (`migrations/011`'s `ml_model_selection` table) so it survives one
too — verified live: activated a freshly trained candidate from the dashboard, confirmed the very
next `/predictions` call returned its version with no new "loaded ML model" startup log line,
then restarted the container and confirmed it came back up on the saved selection instead of
`MODEL_PATH`'s env-var default.

[training/retrain.sh](../training/retrain.sh) automates generating a new candidate (export +
train, both targets) and can be scheduled (cron/systemd timer) to run periodically as more real
history accumulates — see training/README.md's "Regenerating candidates on a schedule". It only
ever writes new, uniquely-versioned files; it never changes which model is active. Promotion is
always the separate, human, Account-page step above — deliberately, not just unfinished: with
training data this thin and skewed (see the caveats above), a person should look at a candidate's
real metrics before it starts influencing real recommendations, the same stance already applied
to walk-forward threshold optimization (see docs/backtesting.md).

## Production Inference
The production system is written in Go (`internal/application/ml`, `OnnxModel`).
The ML model is consumed through a stable interface (`UseCase.Predict(FeatureVector) Prediction`) —
callers never see whether a real model or the stub answered.
The model implementation is replaceable: swapping `MODEL_PATH` to a new version, or unsetting it
entirely, changes nothing else in the calling code.

This is the repo's first cgo dependency (`onnxruntime_go` dlopens the native
`libonnxruntime.so`/`.dylib` at runtime) — the Dockerfile moved off Alpine to a glibc base
(`debian:bookworm-slim`) to safely bundle it; see the Dockerfile's comments for why Alpine/musl
isn't viable here.