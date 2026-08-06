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
backtesting gap — see docs/backtesting.md); and `expected_duration`/`expected_strength` (the
predictions table columns beyond `correction_probability`) still stay NULL — no model produces
those yet.

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