# Atlas Disaster Recovery Runbook

Translates the Backup Strategy, Disaster Recovery, and Production Checklist sections of
[docs/deployment.md](../docs/deployment.md) into concrete, actionable procedures.

In Kubernetes, TimescaleDB backups are now automated (`deploy/backup-cronjob.yaml`): a daily
`CronJob` runs `pg_dump` and keeps the last 7 days of dumps on a `PersistentVolumeClaim`. Docker
Compose has no equivalent scheduler, so the manual procedure below still applies there. Neither
covers copying dumps off-cluster to durable object storage (S3/GCS/etc.) — that's a follow-up
once a target/schedule for it is chosen, same as everything else in this runbook that's tagged
an infrastructure decision rather than a code one.

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

## TimescaleDB backup

Kubernetes (automated — `deploy/backup-cronjob.yaml` runs this on the same daily schedule):

```bash
# Check the automated backup is actually running and succeeding:
kubectl get cronjob atlas-db-backup -n atlas
kubectl get jobs -n atlas -l job-name=atlas-db-backup
# List what's on the PVC (via a throwaway pod, or exec into the most recent job's pod):
kubectl run atlas-backup-browse --rm -it --restart=Never -n atlas \
  --image=busybox --overrides='{"spec":{"containers":[{"name":"atlas-backup-browse","image":"busybox","command":["ls","-lh","/backups"],"volumeMounts":[{"name":"b","mountPath":"/backups"}]}],"volumes":[{"name":"b","persistentVolumeClaim":{"claimName":"atlas-db-backups"}}]}}'
```

Docker Compose (manual — no scheduler in this stack):

```bash
docker compose exec timescaledb pg_dump -U atlas -d atlas -F c -f /tmp/atlas-backup.dump
docker compose cp timescaledb:/tmp/atlas-backup.dump ./atlas-backup-$(date +%F).dump
```

Kubernetes (on-demand, outside the automated daily schedule — e.g. right before a risky change):

```bash
kubectl exec -n atlas deploy/timescaledb -- pg_dump -U atlas -d atlas -F c -f /tmp/atlas-backup.dump
kubectl cp atlas/$(kubectl get pod -n atlas -l app=timescaledb -o jsonpath='{.items[0].metadata.name}'):/tmp/atlas-backup.dump ./atlas-backup-$(date +%F).dump
```

Store the resulting `.dump` file somewhere durable and off-cluster (object storage, etc.) —
this repo doesn't prescribe a specific target since that's an infrastructure/cost decision,
not a code decision.

## TimescaleDB restore

To restore from an automated CronJob backup, first copy the dump off the PVC (the `busybox`
browse command above shows what's there; `kubectl cp` from that same throwaway pod works the
same way as the on-demand commands above). Then, from wherever the `.dump` file ends up:

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

## Infrastructure alerting

Separate from the Discord alerts `internal/app/alerts_loop.go` already sends for recommendation
changes, `prometheus/alerts.yml` defines Prometheus alerting rules for Atlas's own health:

| Alert | Fires when |
|---|---|
| `AtlasAPIDown` | `/metrics` hasn't been scrapeable for 2 minutes |
| `AtlasAPIFlapping` | atlas-api's up/down state changed more than 4 times in 15 minutes (crash-loop proxy that needs no extra scrape config) |
| `AtlasPodCrashLooping` | a Kubernetes pod restarted more than 3 times in 15 minutes — needs kube-state-metrics scraped, which this repo doesn't configure (see `deploy/README.md`'s Scope note); inert until that's added |
| `AtlasHighErrorRate` | more than 5% of requests returned a 5xx status over 5 minutes |

Docker Compose already wires this end to end: `prometheus` loads `alerts.yml` and forwards firing
alerts to the `alertmanager` service (`:9093`), which is configured (`alertmanager/alertmanager.yml`)
with a `"null"` receiver — alerts are visible in Alertmanager's UI but delivered nowhere until a
real receiver (Discord/Slack/webhook/email) replaces it, deliberately left as a config change
rather than a code change (which destination to use is Kris's call, same reasoning as
`deploy/secret.yaml`'s placeholder). To check it's working:

```bash
docker compose up -d prometheus alertmanager
curl -s http://localhost:9090/api/v1/rules | jq '.data.groups[].rules[].name'
curl -s http://localhost:9093/api/v2/status
```

## Production checklist

Copied from `docs/deployment.md`, with the current, honest status of each item in this repo:

- [x] Metrics available — `/metrics` exposes HTTP, provider, decision, ML, and alert counters
- [x] Dashboards created — `grafana/dashboards/overview.json`, provisioned automatically
- [x] Health checks working — `/health`, wired into Docker Compose and the k8s `deployment.yaml` probes
- [x] Logs verified — structured JSON via Zap
- [x] Resource limits configured — see `deploy/deployment.yaml`
- [x] Container security checked — non-root, read-only root filesystem, all capabilities dropped (`deploy/deployment.yaml`); Dockerfile already ran as non-root
- [x] Database backup configured — **automated in Kubernetes** (`deploy/backup-cronjob.yaml`: daily `pg_dump`, 7-day retention on a PVC — see the Backup section above); Docker Compose still only has the manual procedure (no scheduler in that stack). Off-cluster/object-storage copy of the dumps is still a follow-up (see above).
- [ ] Secrets secured — `deploy/secret.yaml` is a placeholder template; real secret management (Sealed Secrets, External Secrets Operator, cloud secret manager) is not wired up
- [x] Alerts configured — Discord business alerts (`internal/app/alerts_loop.go`) plus infrastructure-level Prometheus alerting rules (`prometheus/alerts.yml`: `AtlasAPIDown`, `AtlasAPIFlapping`, `AtlasPodCrashLooping`, `AtlasHighErrorRate`) routed through Alertmanager (`alertmanager/alertmanager.yml`) — but that config's receiver is still a placeholder (see the file's own comment), so alerts fire and are visible in Alertmanager's UI (`:9093`) but are not yet delivered anywhere. Wiring a real receiver (Discord/Slack/webhook/email) is a config change, not a code change.
- [ ] Recovery tested — this runbook has not yet been exercised end-to-end against a real deployment

Do not check the last two boxes until they've actually been done — this table is meant to
stay honest, not to look complete.
