# Backs the Account dashboard's "Train new candidate" button (see cmd/trainer). Deliberately NOT
# modeled after the root Dockerfile's multi-stage/copied-source build: this container is expected
# to run with the full repo bind-mounted read-write at /app (see docker-compose.yml's
# atlas-trainer service), so training/retrain.sh, cmd/export-training-data, and
# training/train.py are always the exact same code a human would run by hand or from cron — no
# image rebuild needed to pick up a training-pipeline change. This is dev/ops tooling, not the
# production atlas-api image; it never depends on this and stays off the host network entirely
# (no published port — see docker-compose.yml's Scope note on that service).
FROM golang:1.26.8-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
      python3 python3-venv python3-pip \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

# Every Go invocation in this container — the CMD below, retrain.sh's `go run
# ./cmd/export-training-data`, and scripts/trainer-smoke-test.sh's build — runs against a
# bind-mounted repo whose .git belongs to the host user, not to this container's. Go stamps
# binaries with VCS metadata by default, git refuses to read a repository it considers
# dubiously-owned, and the build dies with "error obtaining VCS status: exit status 128".
#
# Docker Desktop virtualizes file ownership, so this never appears on macOS — it appeared the
# first time CI ran the smoke test on Linux. Stamping is meaningless here anyway: these are
# throwaway binaries built from a working tree, not released artifacts.
ENV GOFLAGS=-buildvcs=false

CMD ["go", "run", "./cmd/trainer"]
