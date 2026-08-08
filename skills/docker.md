# Docker

- Multi-stage build (`Dockerfile`): a `golang` builder stage compiles the binary, then it's
  copied into a slim runtime stage — no Go toolchain in the shipped image.
- Runtime base is `debian:bookworm-slim`, **not Alpine**. Deliberate, not an oversight:
  `onnxruntime_go` dlopens a native `libonnxruntime.so` at runtime (the repo's only cgo
  dependency), built against glibc — musl (Alpine) can't load it reliably. Don't "optimize" this
  back to Alpine without re-solving that.
- Non-root user must be a **numeric UID**, not a named user: `USER 10001` after
  `useradd --uid 10001 appuser`. Kubernetes' `runAsNonRoot` pod security check can't resolve
  `/etc/passwd` before the container starts, so a named `USER appuser` fails
  `CreateContainerConfigError` on real Kubernetes even though it works under Docker Compose
  (which doesn't enforce `runAsNonRoot` at all) — this broke for real during a recovery-drill
  rehearsal on minikube; see `deploy/RUNBOOK.md`'s Recovery drill section. The same lesson applies
  to any other image without a non-root default user (e.g. the backup CronJob's
  `timescale/timescaledb` image needed an explicit `runAsUser`/`fsGroup` for the same reason).
- Healthcheck hits the real `/health` endpoint (`wget -qO- http://localhost:8080/health`), not a
  process-exists check — same in `docker-compose.yml` and the Kubernetes deployment's liveness
  probe.
- Volumes: `timescaledb-data`/`redis-data`/`prometheus-data`/`grafana-data`/`alertmanager-data`
  are named/persistent; `./training/models:/models:ro` is a read-only bind mount so
  `MODEL_PATH`/`RECOVERY_MODEL_PATH` (or the Account dashboard's model picker — see `ml.md`) can
  reference a locally-trained model without rebuilding the image.
- Graceful shutdown: `cmd/api/main.go` handles `SIGTERM`/`SIGINT` and calls `Application.Close()`
  (releases DB/Redis/ONNX sessions) before exiting.
- Real secrets never live in `docker-compose.yml` — they're `${VAR:-default}` references reading
  from a gitignored `.env` (see `.env.example` for the full documented list). Docker Compose is
  the primary deployment target (see `architecture-final.md`); Kubernetes manifests in `deploy/`
  are real and tested against minikube, but production-cluster testing is explicitly out of
  scope for this project.