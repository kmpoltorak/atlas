# Atlas ML Training

Trains the correction-probability model referenced by `internal/application/ml` and
`docs/ml.md`. This is offline/local tooling — it is not part of the deployed `atlas-api`
container and does not run in CI.

## 1. Collect real history

Atlas only trains on bars it has actually collected via the live market provider (see
PROJECT_CONTEXT.md's Implementation status note — no synthetic/external datasets). Point
Atlas at your watchlist symbols and let it collect a real window of history first, e.g.:

```
curl "http://localhost:8080/prices?symbol=AAPL&window=500"
```

Repeat per symbol you want in the training set. More symbols and a longer window produce more
training rows — with a single symbol at a small window, expect only a few hundred usable rows
after the lookback/horizon are trimmed (see step 2), which is a small first dataset, not a
production-grade one.

## 2. Export features + labels

From the repo root:

```
go run ./cmd/export-training-data --output training/data/features.csv
```

This reads every symbol with persisted bars, computes the same feature vector the live
inference path uses (`internal/application/ml.BuildFeatureVector`), and labels each row with
whether a >5% close-price drop occurred within the next 10 bars
(`internal/application/ml.DefaultCorrectionLabelParams`) — override with `--horizon-bars` /
`--threshold-pct` if you want a different label definition.

## 3. Train and export to ONNX

```
cd training
python3 -m venv .venv && source .venv/bin/activate
pip install -r requirements.txt
python3 train.py --input data/features.csv --output-dir models
```

This writes `models/atlas-ml-<version>.onnx` and a `models/atlas-ml-<version>.json` metadata
sidecar (feature order, holdout AUC/accuracy, label distribution, label definition). Model
files are gitignored — they're a locally/CI-generated artifact, not committed source.

If the printed label distribution is heavily one-sided, or the script exits early because a
split ended up single-class, you don't have enough data yet — collect more history (step 1)
before trusting the model it produces.

## 4. Point Atlas at the model

Via Docker Compose (docker-compose.yml already mounts `./training/models` read-only into the
`atlas-api` container at `/models`, so reference the container path, not the host path):

```
MODEL_PATH=/models/atlas-ml-<version>.onnx docker compose up -d
```

Or running the API directly (no Docker; `LIBONNXRUNTIME_PATH` must point at a real
`libonnxruntime.so`/`.dylib` you have locally — the pip `onnxruntime` package bundles one, e.g.
`.venv/lib/python3.11/site-packages/onnxruntime/capi/libonnxruntime.<version>.dylib`):

```
MODEL_PATH=training/models/atlas-ml-<version>.onnx \
LIBONNXRUNTIME_PATH=/path/to/libonnxruntime.so \
  go run ./cmd/api
```

If `MODEL_PATH` is unset (the default in both cases above) or the file can't be loaded, Atlas
falls back to the existing deterministic RSI-derived stub prediction (logged clearly at
startup) rather than failing to start — see `internal/application/ml`'s doc comments.
