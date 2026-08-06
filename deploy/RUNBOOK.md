# Atlas Disaster Recovery Runbook

Translates the Backup Strategy, Disaster Recovery, and Production Checklist sections of
[docs/deployment.md](../docs/deployment.md) into concrete, actionable procedures.

This is currently a **documented manual procedure**, not an automated one — there is no
scheduled backup job in this repo yet. Automating the TimescaleDB backup (e.g. a Kubernetes
CronJob running `pg_dump`) is a reasonable next step once this runbook has been exercised at
least once for real.

## What needs backing up, and where it already lives

Per `docs/deployment.md`'s Backup Strategy:

| Target | Cadence | Where it lives today |
|---|---|---|
| TimescaleDB (prices, portfolio positions/transactions, watchlist) | Daily | `timescaledb-data` volume — **not yet backed up automatically** |
| Configuration | On every change | This Git repository (`docker-compose.yml`, `deploy/`, `.env` is gitignored by design) |
| Grafana dashboards | On every change | Already in Git: `grafana/dashboards/`, provisioned automatically — a `git checkout` restores them |
| Prometheus config | On every change | Already in Git: `prometheus/prometheus.yml` — a `git checkout` restores it |
| ML models | Versioned storage | Real trained artifacts now exist (`training/models/atlas-ml-<version>.onnx` + `.json` metadata sidecar — see docs/ml.md), but they're gitignored (locally/CI-generated, not committed source) and have **no backup/versioned-storage procedure yet** — a lost model directory means re-running `training/` end to end, which is reproducible from TimescaleDB's own price history but not instant. Atlas runs without a model at all until `MODEL_PATH` is set (falls back to the deterministic RSI stub), so this isn't yet a service-availability risk, just a lost-work risk. |

The practical takeaway: **TimescaleDB is the only thing with an active backup procedure today.**
Everything else recovers by redeploying from this Git repository — except ML model artifacts,
which recover by re-running training/ (reproducible, not instant) since they aren't committed
or backed up anywhere yet.

## TimescaleDB backup (manual, until automated)

Docker Compose:

```bash
docker compose exec timescaledb pg_dump -U atlas -d atlas -F c -f /tmp/atlas-backup.dump
docker compose cp timescaledb:/tmp/atlas-backup.dump ./atlas-backup-$(date +%F).dump
```

Kubernetes:

```bash
kubectl exec -n atlas deploy/timescaledb -- pg_dump -U atlas -d atlas -F c -f /tmp/atlas-backup.dump
kubectl cp atlas/$(kubectl get pod -n atlas -l app=timescaledb -o jsonpath='{.items[0].metadata.name}'):/tmp/atlas-backup.dump ./atlas-backup-$(date +%F).dump
```

Store the resulting `.dump` file somewhere durable and off-cluster (object storage, etc.) —
this repo doesn't prescribe a specific target since that's an infrastructure/cost decision,
not a code decision.

## TimescaleDB restore

```bash
pg_restore -U atlas -d atlas -c ./atlas-backup-<date>.dump
```

Verify after restore:

```bash
curl -s http://<atlas-api>/health   # expect {"status":"healthy", ...}
curl -s http://<atlas-api>/prices?symbol=AAPL
```

## Recovery priority (per docs/deployment.md)

1. **Database** — restore the most recent TimescaleDB dump (see above). Nothing else is
   useful without it.
2. **Configuration** — `git checkout` this repository at the last known-good commit;
   `docker-compose.yml` / `deploy/` define the rest of the stack declaratively.
3. **Application** — redeploy `atlas-api` (`docker compose up -d atlas-api` or
   `kubectl rollout restart deployment/atlas-api -n atlas`).
4. **ML models** — not yet applicable (see table above); revisit once real trained models
   exist.

## Production checklist

Copied from `docs/deployment.md`, with the current, honest status of each item in this repo:

- [x] Metrics available — `/metrics` exposes HTTP, provider, decision, ML, and alert counters
- [x] Dashboards created — `grafana/dashboards/overview.json`, provisioned automatically
- [x] Health checks working — `/health`, wired into Docker Compose and the k8s `deployment.yaml` probes
- [x] Logs verified — structured JSON via Zap
- [x] Resource limits configured — see `deploy/deployment.yaml`
- [x] Container security checked — non-root, read-only root filesystem, all capabilities dropped (`deploy/deployment.yaml`); Dockerfile already ran as non-root
- [ ] Database backup configured — **manual procedure only, not scheduled** (see above)
- [ ] Secrets secured — `deploy/secret.yaml` is a placeholder template; real secret management (Sealed Secrets, External Secrets Operator, cloud secret manager) is not wired up
- [ ] Alerts configured — Discord alert notifications exist (`internal/app/alerts_loop.go`), but no Prometheus **alerting rules** (Alertmanager) exist yet for infrastructure-level alerts (pod crash-looping, high error rate, etc.)
- [ ] Recovery tested — this runbook has not yet been exercised end-to-end against a real deployment

Do not check the last four boxes until they've actually been done — this table is meant to
stay honest, not to look complete.
