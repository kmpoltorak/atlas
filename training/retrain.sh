#!/usr/bin/env bash
# Periodically regenerates candidate correction/recovery models from whatever real history Atlas
# has collected since the last run — see training/README.md for the manual, step-by-step version
# of exactly what this automates. Intended to be run on a schedule (cron/systemd timer) against a
# host that can reach the TimescaleDB Atlas actually writes to.
#
# This script only ever produces new, uniquely-versioned files under training/models/ — it never
# touches which model is active. A new candidate becomes the live model only when a human picks
# it from the Account dashboard page (/dashboard/account) after reviewing its printed holdout
# metrics — the same "never auto-promote" stance this project already applies to walk-forward
# threshold optimization (see docs/backtesting.md). A cron job silently swapping the model behind
# real investment recommendations, with data this thin (see docs/ml.md's caveats), would be the
# wrong kind of automation for what Atlas is.
set -euo pipefail
cd "$(dirname "$0")/.."

# Host-execution default: docker-compose.yml's DATABASE_HOST default ("timescaledb") only
# resolves inside the Compose network. Override any of these in the environment before calling
# this script if your database lives somewhere else.
export DATABASE_HOST="${DATABASE_HOST:-localhost}"
export DATABASE_PORT="${DATABASE_PORT:-5432}"
export DATABASE_NAME="${DATABASE_NAME:-atlas}"
export DATABASE_USER="${DATABASE_USER:-atlas}"
export DATABASE_PASSWORD="${DATABASE_PASSWORD:-atlas}"

# VENV_DIR defaults to the same training/.venv the manual steps in training/README.md use, but
# is overridable (see docker-compose.yml's atlas-trainer service) so a containerized run doesn't
# reuse a venv created on a different OS — a venv's python3 is a symlink to the interpreter that
# created it, which doesn't resolve inside a container built on a different base OS (confirmed
# live: a macOS-created training/.venv bind-mounted into the Linux atlas-trainer container fails
# with "training/.venv/bin/python3: No such file or directory").
VENV_DIR="${TRAINING_VENV_DIR:-training/.venv}"

echo "==> Exporting fresh training data"
go run ./cmd/export-training-data -label-type=correction --output training/data/features.csv
go run ./cmd/export-training-data -label-type=recovery --output training/data/recovery.csv

if [ ! -x "$VENV_DIR/bin/python3" ]; then
  echo "==> Bootstrapping $VENV_DIR (first run, or previous venv isn't usable here)"
  rm -rf "$VENV_DIR"
  python3 -m venv "$VENV_DIR"
  "$VENV_DIR/bin/pip" install --quiet -r training/requirements.txt
fi

echo "==> Training candidate models"
"$VENV_DIR/bin/python3" training/train.py --input training/data/features.csv --output-dir training/models --target correction
"$VENV_DIR/bin/python3" training/train.py --input training/data/recovery.csv --output-dir training/models --target recovery

echo
echo "==> Done. New candidate model files are listed above (wrote ...)."
echo "    Nothing was activated — review each one's printed holdout AUC/accuracy, then pick it"
echo "    from /dashboard/account if it looks good. Old candidates are never deleted or"
echo "    overwritten automatically; prune training/models/ by hand once you no longer need them."

if [ -n "${TRAINING_DISCORD_WEBHOOK_URL:-}" ]; then
  latest_correction=$(ls -t training/models/atlas-ml-v*.json 2>/dev/null | head -n1 || true)
  latest_recovery=$(ls -t training/models/atlas-ml-recovery-v*.json 2>/dev/null | head -n1 || true)
  message="Atlas: new ML training candidates ready for review. correction=${latest_correction:-none} recovery=${latest_recovery:-none}. Activate from /dashboard/account if they look good."
  curl -fsS -H "Content-Type: application/json" \
    -d "$(printf '{"content": %s}' "$(printf '%s' "$message" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))')")" \
    "$TRAINING_DISCORD_WEBHOOK_URL" >/dev/null || echo "==> Discord notification failed (non-fatal)"
fi
