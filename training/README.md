# Atlas ML Training

Trains the correction-probability and recovery-probability models referenced by
`internal/application/ml` and `docs/ml.md` — two genuinely separate, independently-optional
models trained by the identical procedure below, just pointed at a different exported CSV
(`--label-type`/`--target`). This is offline/local tooling — it is not part of the deployed
`atlas-api` container and does not run in CI.

## 1. Collect real history

Atlas only trains on bars it has actually collected via the live market provider (no
synthetic or external datasets — see docs/ml.md). Point
Atlas at your watchlist symbols and let it collect a real window of history first, e.g.:

```
curl "http://localhost:8080/prices?symbol=AAPL&window=500"
```

Repeat per symbol you want in the training set. More symbols and a longer window produce more
training rows — with a single symbol at a small window, expect only a few hundred usable rows
after the lookback/horizon are trimmed (see step 2), which is a small first dataset, not a
production-grade one.

## 2. Export features + labels

From the repo root, for the correction model (default):

```
go run ./cmd/export-training-data -label-type=correction --output training/data/features.csv
```

This reads every symbol with persisted bars, computes the same feature vector the live
inference path uses (`internal/application/ml.BuildFeatureVector`), and labels each row with
whether the close dropped past a volatility-scaled barrier within the next 10 bars — 4x the
instrument's own trailing 20-bar realised volatility, floored at 2%
(`internal/application/ml.DefaultCorrectionLabelParams`). Override the horizon with
`-horizon-bars`; `-threshold-pct` sets the fixed-percentage fallback, which only applies when the
volatility multiple is disabled. A fixed percentage asked a different question of every
instrument, which is most of why a per-symbol frequency table was so hard to beat — see
`docs/ml.md`.

The CSV feature columns are `rsi, ema_dist, macd_pct, tr_pct, atr_pct, rvol_20, rvol_50, volume`
— the same order as `internal/application/ml.FeatureOrder`, which `train.py` and the ONNX
sidecar guard must agree with exactly. Every one is scale-free (a ratio or a bounded index),
never a raw price or share count. If you are looking at an older CSV with
`ema, macd, atr` columns instead, it predates that change and `train.py` will refuse it: those
carried each symbol's absolute price level, which let the model identify the symbol instead of
reading its dynamics. See `internal/domain/ml.FeatureVector`'s doc comment for the measurement
that established this. Re-run this command to regenerate.

For the recovery model, `-label-type=recovery` instead — only rows where a correction actually
occurred get a label at all (recovery isn't a meaningful question without one — see
`internal/application/ml.ComputeRecoveryLabel`'s doc comment), so expect far fewer rows than the
correction export above, and expect that shortfall to bite harder here: the correction dataset
is thin by ML standards but at least draws from every bar; the recovery dataset is thin *and*
conditioned on a rarer event, so treat any model trained on it with real caution, not just the
usual small-dataset caveat:

```
go run ./cmd/export-training-data -label-type=recovery --output training/data/recovery.csv
```

## 3. Train and export to ONNX

The pinned requirements target Python 3.11 (the trainer image's). With a newer host Python, run
these inside the `atlas-trainer` container or a `python:3.11-slim-bookworm` one instead.

```
cd training
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
python3 train.py --input data/features.csv --output-dir models --target correction
python3 train.py --input data/recovery.csv --output-dir models --target recovery
```

Both runs use the identical training procedure — same XGBoost params, same **global**
time-ordered holdout with a `label_end` purge at the boundary, same ONNX export — only the input
CSV, `--target` (which affects the output filename prefix, the metadata's `label_definition` text
and the default embargo width) differ. Each writes `models/atlas-ml[-recovery]-<version>.onnx` and a matching
`.json` metadata sidecar (feature order, holdout AUC / PR AUC / accuracy, baselines, label
distribution, label definition). Model files are gitignored — they're a locally/CI-generated
artifact, not committed source.

### The split lives in its own module

`train.py` delegates the train/holdout split to `split.py`, which imports nothing but pandas.
That boundary is deliberate. The split used to live in `train.py` alongside lazy, in-function
imports of sklearn and xgboost so the file stayed importable without the training stack — until a
function referenced a name whose lazy import lived in a *different* function, and a real run died
with `NameError: name 'roc_auc_score' is not defined` after the export and the split had both
already completed. A module boundary cannot fail that way: a name is either imported at the top
of the file or it is not.

`test_split.py` covers the split itself and needs only pandas, so it runs anywhere:

```
cd training && python3 -m pytest test_split.py
```

CI goes further and runs a **full training pass** on every push — `scripts/trainer-smoke-test.sh`
builds the trainer container, generates a synthetic CSV shaped exactly like the exporter's output
(`make_synthetic_features.py`), and trains a real model from it, asserting the ONNX file and its
metadata sidecar come out intact. The earlier smoke test only checked that `train.py` *parsed*,
which is precisely why the `NameError` above shipped. Synthetic data is used here and nowhere
else: it never produces a candidate anyone can promote.

### Reading the printed metrics

A few numbers matter, and **accuracy is not one of them**. Corrections are rare, so "always
predict no correction" already scores a high accuracy (the run prints it as the majority-class
baseline); a model can look excellent on that figure while being useless. Each run therefore
prints its own baselines alongside every metric:

- **PR AUC vs the symbol-base-rate baseline — the verdict.** The baseline is a predictor using *no
  features at all*, only each symbol's positive rate in the training data. It is the bar to clear:
  a model at or below it has learned which symbols tend to fall, not what a fall looks like.
  `train.py` judges this on PR AUC (average precision) because Atlas alerts on the top of the
  ranking, prints a loud WARNING and records `beats_symbol_base_rate: false` in the sidecar when
  the model loses, and the Account page's model picker repeats it as "DOES NOT BEAT BASELINE".
- **ROC AUC vs the same baseline**, reported for context. It is dominated by the uneventful middle
  of the ranking that no alert touches, so it can disagree with PR AUC — and when it does, PR AUC
  is the one that describes what Atlas does with the model.
- **accuracy@0.5 vs the majority-class baseline**, reported only for continuity.
- **Brier score, with the mean predicted probability next to the actual rate.** Atlas consumes
  this output as a probability (the decision engine blends `1 - correction_probability`, and the
  dashboard shows it to a human), so being *calibrated* matters as much as ranking well. This is
  why training deliberately runs without `scale_pos_weight` despite the class imbalance — see
  `train.py`'s comment on the `XGBClassifier` call for the measurement behind that choice.

An embargo of `--embargo-bars` rows is dropped at each symbol's train/holdout boundary, because
a label looks forward: without the gap, the last training rows' labels are computed from bars
sitting in the holdout, and the holdout score comes out flattering. The default matches the
label horizon for the target (10 correction, 30 recovery) — raise it to match if you export with
`--horizon-bars`/`--recovery-horizon-bars` above those.

If the printed label distribution is heavily one-sided, or the script exits early because a
split ended up single-class or the embargo consumed the holdout, you don't have enough data yet
— collect more history (step 1) before trusting the model it produces.

## 4. Point Atlas at the model(s)

`MODEL_PATH` (correction) and `RECOVERY_MODEL_PATH` (recovery) are separate, independently
optional env vars — set either, both, or neither. Via Docker Compose (docker-compose.yml
already mounts `./training/models` read-only into the `atlas-api` container at `/models`, so
reference the container path, not the host path):

```
MODEL_PATH=/models/atlas-ml-<version>.onnx \
RECOVERY_MODEL_PATH=/models/atlas-ml-recovery-<version>.onnx \
  docker compose up -d
```

Or running the API directly (no Docker; `LIBONNXRUNTIME_PATH` must point at a real
`libonnxruntime.so`/`.dylib` you have locally — the pip `onnxruntime` package bundles one, e.g.
`.venv/lib/python3.11/site-packages/onnxruntime/capi/libonnxruntime.<version>.dylib`):

```
MODEL_PATH=training/models/atlas-ml-<version>.onnx \
RECOVERY_MODEL_PATH=training/models/atlas-ml-recovery-<version>.onnx \
LIBONNXRUNTIME_PATH=/path/to/libonnxruntime.so \
  go run ./cmd/api
```

If either path is unset (the default in both cases above) or its file can't be loaded, Atlas
falls back to the existing deterministic RSI-derived stub prediction for correction / leaves
`recovery_probability` at 0 for recovery (both logged clearly at startup) rather than failing to
start — see `internal/application/ml`'s doc comments.

## 5. Picking a model from the dashboard, without a restart

Instead of (or in addition to) `MODEL_PATH`/`RECOVERY_MODEL_PATH`, any trained model already
sitting in `training/models/` (i.e. under `ML_MODELS_DIR`, `/models` inside the container) can be
activated from the **Account** dashboard page (`/dashboard/account`) — it lists every model it
finds a metadata sidecar for, with the same real holdout AUC/accuracy/row-count numbers step 3
above printed, and swaps the picked one into live inference immediately (no restart) via
`ml.UseCase.SetModel`/`SetRecoveryModel`. The choice is durably recorded (`migrations/011`) so it
survives a restart too — at startup, a saved dashboard selection takes priority over
`MODEL_PATH`/`RECOVERY_MODEL_PATH`'s env-var default, falling back to it only if the selected
file no longer loads.

The same page also has a "Remove" button next to each model to delete old candidates you no
longer need — this needs the atlas-trainer service (step 7 below), since atlas-api's own
`training/models` mount is read-only by design. Refuses to delete whichever model is currently
active for that role, so you can't pull the rug out from under live inference by mistake.

## 6. Regenerating candidates on a schedule

[retrain.sh](retrain.sh) automates steps 1–3 above end to end (export both CSVs, train both
targets) and writes new, uniquely-versioned files without ever touching or deleting an existing
one:

```
./training/retrain.sh
```

Run it by hand whenever you want a fresh candidate, or schedule it (cron/systemd timer) to
regenerate candidates periodically as more real history accumulates — e.g. a weekly cron entry:

```
0 3 * * 0 cd /path/to/atlas && DATABASE_HOST=localhost ./training/retrain.sh >> /var/log/atlas-retrain.log 2>&1
```

It never changes which model is active — that's always the manual step-4-or-5 decision above. It
only logs which candidate files were written ("New candidates: correction=... recovery=...") —
there is no success notification anymore. A "new candidate ready" ping used to go to
`TRAINING_DISCORD_WEBHOOK_URL`, but it fired on every single run regardless of whether the
candidate actually beat the baseline, which made it noise, not signal — check the printed metrics
in the log/output instead. A *failed* run triggered from the dashboard is still reported: see
section 7's note on `cmd/trainer`'s own failure notification (`set -euo pipefail` means this script
can't notify its own failure either way).

## 7. Triggering training from the dashboard

retrain.sh can also be triggered on demand from the Account page's "Train new candidate" button
instead of a shell/cron — it calls a small internal-only service (`cmd/trainer`) that just runs
retrain.sh and reports back its status/output. This lives in its own container
(`training/Dockerfile`), never inside `atlas-api`'s own image: retrain.sh needs the Go toolchain
and a Python virtualenv at runtime, and the production image is explicitly not supposed to depend
on either: Python trains, Go infers.

It is part of the default stack: `docker compose up -d` starts it, and atlas-api reaches it at
`TRAINER_URL=http://atlas-trainer:8091` (the Compose default). Set `TRAINER_URL=` to an empty
value in `.env` to hide the dashboard buttons; the Account page then explains how to run
retrain.sh by hand, and the manual/cron path above keeps working either way.

Set `ALERTMANAGER_DISCORD_WEBHOOK_URL` in `.env` (docker-compose.yml passes it through as
`TRAINING_DISCORD_WEBHOOK_URL`) to be notified when a dashboard-triggered run fails — `cmd/trainer`
sends that message itself, the one outcome retrain.sh's own `set -e` can never report. A
successful run only logs (see section 6 above). Leaving it unset just means no failure
notification either (non-fatal, like every optional integration in Atlas).

The trainer container bind-mounts the whole repo read-write (not a copied-at-build-time image),
so it always runs the exact same `cmd/export-training-data`/`training/train.py` code a human
would — no rebuild needed to pick up a training-pipeline change. It uses its own venv directory
(`TRAINING_VENV_DIR=training/.venv-container`, gitignored), separate from the one the manual
steps above create: a venv's `python3` is a symlink back to whichever interpreter created it,
which doesn't resolve if a venv created on your host OS gets bind-mounted into this (Linux)
container — confirmed live running this on macOS. The first run through the container bootstraps
its own venv from scratch (installing pandas/numpy/scikit-learn/xgboost/onnx/onnxmltools/
onnxruntime), which takes a few minutes; every run after that reuses it and is much faster.
