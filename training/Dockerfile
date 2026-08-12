# Backs the Account dashboard's "Train new candidate" button (see cmd/trainer). Deliberately NOT
# modeled after the root Dockerfile's multi-stage/copied-source build: this container is expected
# to run with the full repo bind-mounted read-write at /app (see docker-compose.yml's
# atlas-trainer service), so training/retrain.sh, cmd/export-training-data, and
# training/train.py are always the exact same code a human would run by hand or from cron — no
# image rebuild needed to pick up a training-pipeline change. This is dev/ops tooling, not the
# production atlas-api image; it never depends on this and stays off the host network entirely
# (no published port — see docker-compose.yml's Scope note on that service).
FROM golang:1.26.1-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
      python3 python3-venv python3-pip \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
CMD ["go", "run", "./cmd/trainer"]
