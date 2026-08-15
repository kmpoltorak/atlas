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
collected (see PROJECT_CONTEXT.md); validation is a per-symbol time-ordered holdout with an
embargo gap, not full walk-forward validation (a separate, larger backtesting gap, now real —
see docs/backtesting.md); `expected_duration`/`expected_strength` (the predictions table columns
beyond `correction_probability`) still stay NULL, since no model produces those yet; and, most
importantly, both models are judged on PR AUC against a per-symbol base-rate baseline rather than on
accuracy, which on this target is close to meaningless — see "Current numbers, and what they
say" below.

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

## Feature scaling, and the modelling bug it fixed

Every feature is **scale-free** — a ratio or a bounded index, never a raw price or share count:
`rsi` (0-100), `ema_dist` ((close-ema)/ema), `macd_pct` (macd/close), `tr_pct` (single-bar true
range/close), `atr_pct` (14-period Wilder ATR/close), `rvol_20`/`rvol_50` (stdev of close-to-close
returns over 20/50 bars), `volume` (bar volume / mean volume). The conversion happens in
`ml.BuildFeatureVector` and nowhere else — the analyzer keeps returning price-unit series for
`/prices`, the decision engine and the persisted `indicators` table, which all legitimately want
them that way.

This was a real fix, and the measurement behind it is worth recording because it is exactly the
kind of failure that hides behind a good-looking metric. The original feature set fed the
analyzer's raw `ema`/`macd`/`atr` straight to the model. Those carry the symbol's absolute price
level: across the collected universe EMA ranged from ~9 (PGE.WA) to ~21,957 (LPP.WA), so any
tree threshold on it was an exact symbol lookup. Per-symbol correction base rates range from
0.00 (GLD, SPY, QQQ, IAU, IBIT, GLDM) to 0.58 (SOXL), so memorising symbol identity was a highly
effective strategy — and the model took it. On the same holdout, a predictor using **no features
at all**, only each symbol's historical correction rate, scored **AUC 0.757 against the trained
model's 0.707**. Meanwhile the reported accuracy of 0.919 was *worse* than always predicting "no
correction" (0.935) on a target where only ~6.5% of bars qualify.

Two structural problems were fixed alongside it:

- **Train/holdout boundary leak.** The split cut each symbol at 80%, but a label looks 10 bars
  forward, so ~470 training rows had labels computed from bars sitting in the holdout.
  `train.py` now drops an embargo gap (`--embargo-bars`, defaulting to the label horizon: 10
  correction, 30 recovery) at every symbol's boundary.
- **Overlapping labels inflate the sample.** One drop event labels ~10 consecutive hourly bars,
  so 1,097 positive rows correspond to only ~178 distinct events. Model capacity was cut
  accordingly (shallower trees, subsampling, L2) — the previous unregularised 300×depth-4 fit was
  fitting noise against ~178 effective positives.

## Current numbers, and what they say

Measured on the current real dataset (10,002 correction rows / 1,036 recovery rows across 47
symbols, real Yahoo 1h bars out of TimescaleDB), with scale-free features, the volatility
features and the embargo in place:

| | PR AUC | base-rate baseline | no-skill | ROC AUC | unseen symbols | Brier |
|---|---|---|---|---|---|---|
| Correction | **0.297** | 0.226 | 0.070 | 0.785 | 0.766 | 0.059 |
| Recovery | **0.758** | 0.528 | 0.427 | 0.782 | 0.566 | 0.183 |

These are measured on the volatility-scaled label (see below); the fixed-5% label the project
started with gave correction PR AUC 0.264 against a 0.188 baseline and ROC AUC 0.710.

**Both models now beat the symbol-base-rate baseline on PR AUC**, which is the metric this
project judges promotion on. That choice is deliberate and the two metrics genuinely disagree
for the correction model: it trails the baseline on ROC AUC (0.710 vs 0.748) while beating it on
PR AUC (0.264 vs 0.188). ROC AUC is dominated by how well the vast, uneventful middle of the
distribution is ordered — and Atlas never alerts on the middle. It alerts on the top of the
ranking, which is exactly what PR AUC measures.

What that looks like in the terms the product is actually built for. Restricting alerts to the
highest-scoring bars in the holdout:

| alerts fired | correct | precision | lift vs 6.5% base rate | baseline at same budget |
|---|---|---|---|---|
| 25 | 17 | **68.0%** | 10.5x | 16.0% |
| 50 | 26 | 52.0% | 8.0x | 38.0% |
| 100 | 39 | 39.0% | 6.0x | 23.0% |

There is also a structural reason the baseline cannot be the product no matter what it scores:
**it is one constant per symbol.** It ranks instruments by how often they historically fell and
can never say a correction is likelier now than yesterday — which is the entire question Atlas
exists to answer on a 1h timeframe. The model's output moves with market state (over one holdout
period SOXL ranged 0.251-0.619 and MSFT 0.020-0.287, against flat baselines of 0.711 and 0.025),
and it scores instruments it has never trained on at 0.732 ROC AUC where the baseline scores
exactly 0.500 by construction, having no history for a newly-watchlisted ticker.

### What made the difference

Volatility, expressed honestly. The pre-fix model had exactly one volatility input, `tr_pct` —
the true range of a *single bar*, which is far too noisy to represent an instrument's character
(note `analyzer.ComputeATR` returns raw per-bar true range despite its name; `ComputeSmoothedATR`
was added for this). Adding a properly smoothed `atr_pct` alone lifted holdout ROC AUC from 0.675
to 0.782 in prototyping — the largest single improvement any change here has produced — and
adding `rvol_20`/`rvol_50` alongside it roughly doubled PR AUC.

That is not a coincidence. Volatility is precisely what separates instruments that correct often
from ones that never do (SOXL corrected in 58% of windows, GLD in 0%), so it was *the* thing the
old model was reaching for when it memorised symbol identity. Giving it a real, measurable
volatility feature replaced the shortcut with the genuine article — which is why the model now
generalises to symbols it has never seen, something memorisation can never do.

Longer volatility windows were tried and rejected on evidence: most symbols hold only ~125 bars,
so a 100-bar rolling window discarded a third of the dataset and made every metric worse.

### The label itself was the deeper problem

Scaling the *features* by volatility helped; the label was still asking a different question of
every instrument. A fixed 5% barrier meant GLD never once qualified in ten bars while SOXL did in
58% of windows, so "will this correct" was substantially "is this a volatile ticker" — which is
precisely why a per-symbol frequency lookup was so hard to beat. Volatility-scaled barriers are
the standard remedy (the triple-barrier method's), and `CorrectionLabelParams` now defaults to a
drop of 4x the instrument's own trailing 20-bar volatility, floored at 2%.

The multiple was measured across the collected universe, not guessed:

| rule | overall positive rate | spread across symbols | symbols never triggering | symbols over 40% |
|---|---|---|---|---|
| fixed 5% | 0.110 | 0.168 | 12 | 6 |
| vol x3.0 | 0.185 | 0.102 | 1 | 1 |
| **vol x4.0** | **0.132** | **0.081** | **1** | **0** |

4x keeps the overall rate close to the rule it replaces while halving the between-symbol spread,
and takes the count of instruments that could never produce a positive example from 12 to 1. It
also made ~6,500 more rows usable, since calm instruments now contribute examples at all. Every
downstream metric improved: correction ROC AUC 0.710 to 0.785, PR AUC 0.264 to 0.297, unseen-
symbol AUC 0.732 to 0.766, Brier 0.071 to 0.059.

The old definition — a flat 5% drop within 10 bars, `CorrectionLabelParams{HorizonBars: 10,
ThresholdPct: 0.05}` — no longer has a named constructor in `labels.go`. Nothing in production
built one, so it now lives only in `labels_test.go`, where it is what the volatility-scaled
default gets contrasted against; models trained against it stay describable from that literal.

Accuracy is reported but never used as a verdict. On a 6.5%-positive target it is close to
meaningless, and it was the number that made the broken model look strong. Training also runs
without `scale_pos_weight` despite the imbalance: measured, rebalancing bought ~0.01 ROC AUC
(inside the noise) while tripling the Brier score, 0.211 vs 0.067, and pushing mean predicted
probability to 0.43 against a true 0.065. Atlas consumes this output *as a probability* — the
decision engine blends `1 - correction_probability` and the dashboard shows it to a person — so
calibration is worth more here than a hundredth of AUC.

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
and serving can never drift apart on the feature math — including the normalisation described
above, which lives in `BuildFeatureVector` precisely so both sides get it identically. Python
only trains on the resulting CSV and computes the label (also computed in Go, from real forward
price action — see `ml.ComputeCorrectionLabel`).

Optuna (hyperparameter search) is not implemented — the current pipeline uses fixed, deliberately
conservative XGBoost hyperparameters. That remains the right priority order, and this pass is the
evidence for it: the correction model's large jump came entirely from adding three volatility
features, not from tuning. The feature set is still where the remaining headroom is.

One known remaining train/serve skew: `volume` normalises against the mean volume of whatever
bars it is given, which is every bar up to `i` during training (an expanding window) but the
~`COLLECTOR_WINDOW_DAYS` collection window at inference. A fixed trailing window would make the
two agree; changing `analyzer.ComputeVolume` also affects the decision engine and the persisted
indicators table, so it is deliberately left as a documented gap rather than a silent change.

## Model Lifecycle
1. Collect historical data — via Atlas's existing collector (`GET /prices`), not a separate
   ingestion path.
2. Generate features — `cmd/export-training-data` (Go, reuses live feature code).
3. Train model — `training/train.py` (XGBoost, time-ordered holdout).
4. Validate model — holdout ROC AUC against the symbol-base-rate baseline, PR AUC, Brier score,
   accuracy, and label-balance checks, printed and written to the
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
`ML_MODELS_DIR` has a metadata sidecar for (real holdout AUC against its baseline, accuracy and
row count, not fabricated — and an explicit "DOES NOT BEAT BASELINE" marker when it applies),
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
always the separate, human, Account-page step above — deliberately, not just unfinished: a person
should look at a candidate's real metrics before it starts influencing real recommendations, the
same stance already applied to walk-forward threshold optimization (see docs/backtesting.md).
That review is only as good as what the picker shows, which is why it leads with AUC against the
symbol-base-rate baseline rather than accuracy — the correction model that had learned nothing
transferable still reported 0.92 accuracy. It also shows the unseen-symbol score, which is what a
freshly-watchlisted ticker actually gets.

Training and deleting old candidates can also both be triggered from the Account page itself, via
a small internal-only sidecar (`cmd/trainer`, the `atlas-trainer` service — off by default,
`docker compose --profile trainer up -d`, `TRAINER_URL`). It runs retrain.sh on demand and reports
real status/output back to the dashboard, and can delete a model's `.onnx`+`.json` files —
`atlas-api`'s own model mount is read-only by design, so deletion has to go through the one
service with write access to `training/models/`. Deleting whichever model is currently active for
a role is refused, not silently allowed. Verified live: triggered training from the dashboard
button, watched it install its own venv and finish for real (`exit_code: 0`, printed holdout
metrics), saw the new candidates appear in the picker, then deleted an old one and confirmed both
files were gone while an attempt to delete the active model was rejected.

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