#!/usr/bin/env bash
# Smoke test for the atlas-trainer container: does the image actually contain a working training
# pipeline?
#
# The 2026-09-05 review's point was that nothing verified this at all. The trainer is built from a
# different base image than atlas-api, installs its Python dependencies at runtime into a venv,
# and is only exercised when a human presses "Train new candidate" — so a broken Dockerfile, a
# dependency that no longer resolves, or a train.py that fails to import would be discovered by
# the one person who eventually clicked the button, months later.
#
# It runs a full training pass on synthetic data (make_synthetic_features.py), not just an import
# check. The earlier version only verified that train.py *parsed*, which is exactly why a
# `NameError: name 'roc_auc_score' is not defined` in a function body shipped and killed a real
# run: py_compile cannot see an undefined name inside a function. Synthetic input keeps this a
# container test rather than a data test — nothing here is ever promoted to a candidate.
set -euo pipefail
cd "$(dirname "$0")/.."

IMAGE="${TRAINER_IMAGE:-atlas-trainer:smoke}"

echo "==> Building the trainer image"
docker build -f training/Dockerfile -t "$IMAGE" .

# bash -c, not bash -lc: a login shell resets PATH from /etc/profile and loses the Go toolchain
# the golang base image puts on it — which is exactly the kind of thing this test exists to catch.
run() {
  docker run --rm -v "$PWD":/app -w /app "$IMAGE" bash -c "$1"
}

echo "==> Python interpreter and venv support"
run 'python3 --version && python3 -m venv --help >/dev/null'

echo "==> The Go exporter compiles inside the image"
# -o /tmp so the bind-mounted repo doesn't collect a stray binary.
run 'go build -o /tmp/export-training-data ./cmd/export-training-data'

# One container invocation for everything that needs the training venv: each `run` is a separate
# `docker run --rm`, so a venv created in one does not exist in the next.
echo "==> Dependencies install, imports resolve, and a full training run completes"
run 'set -e
     python3 -m venv /tmp/venv
     /tmp/venv/bin/pip install --quiet --upgrade pip
     /tmp/venv/bin/pip install --quiet -r training/requirements.txt

     # Version drift between xgboost, onnx and onnxmltools has broken conversion before (see
     # training/requirements.txt) — importing them together is the check that matters.
     /tmp/venv/bin/python -c "
import xgboost, onnx, onnxmltools, sklearn, pandas, numpy
from onnxmltools.convert import convert_xgboost
from onnxmltools.convert.common.data_types import FloatTensorType
print(\"xgboost\", xgboost.__version__, \"onnx\", onnx.__version__, \"onnxmltools\", onnxmltools.__version__)
"

     # Running, not parsing. A NameError inside a function body is invisible to py_compile and
     # only surfaces when that line executes — which is exactly what shipped on 2026-09-06
     # (symbol_base_rate_scores referencing roc_auc_score with no import in scope), failing
     # minutes into a real run, after the export and the split had already succeeded.
     #
     # The data is synthetic and the model is thrown away; what is tested is that every code path
     # from CSV to exported .onnx executes.
     /tmp/venv/bin/python training/make_synthetic_features.py --output /tmp/synthetic.csv
     # A label-parameter sidecar of the shape cmd/export-training-data writes, so the run covers
     # the path that carries the real barrier into the model metadata — a model that cannot say
     # which barrier produced its labels is how a fixed-5% dataset once shipped described as
     # volatility-scaled.
     printf "%s\n" "{\"label_type\":\"correction\",\"horizon_bars\":10,\"threshold_pct\":0.05,\"volatility_multiple\":4.0,\"volatility_window\":20,\"min_threshold_pct\":0.02}" > /tmp/synthetic.csv.params.json
     /tmp/venv/bin/python training/train.py \
       --input /tmp/synthetic.csv --output-dir /tmp/smoke-models --target correction
     test -n "$(ls /tmp/smoke-models/*.onnx 2>/dev/null)" || { echo "no ONNX model was exported"; exit 1; }
     /tmp/venv/bin/python -c "
import json, glob, sys
meta = json.load(open(sorted(glob.glob(\"/tmp/smoke-models/*.json\"))[-1]))
for key in (\"version\", \"feature_order\", \"train_label_end\", \"holdout_start\", \"validation_method\", \"label_params\"):
    if key not in meta:
        sys.exit(f\"metadata sidecar is missing {key}\")
if (meta[\"label_params\"] or {}).get(\"volatility_multiple\") != 4.0:
    sys.exit(\"the model metadata does not carry the label parameters the export used\")
print(\"metadata ok:\", meta[\"version\"], meta[\"validation_method\"])
"'

echo "==> The split logic passes on pandas alone, with no training stack installed"
run 'python3 -m venv /tmp/split-venv \
     && /tmp/split-venv/bin/pip install --quiet pandas==2.2.3 numpy==1.26.4 \
     && /tmp/split-venv/bin/python -m unittest discover -s training -p "test_*.py"'

echo "==> retrain.sh is executable and syntactically valid"
run 'bash -n training/retrain.sh && test -x training/retrain.sh'

echo
echo "Trainer container smoke test passed."
