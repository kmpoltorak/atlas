# Atlas Disaster Recovery Runbook

Translates the Backup Strategy, Disaster Recovery, and Production Checklist sections of
[docs/deployment.md](../docs/deployment.md) into concrete, actionable procedures.

In Kubernetes, TimescaleDB backups are now automated (`deploy/backup-cronjob.yaml`): a daily
`CronJob` runs `pg_dump` and keeps the last 7 days of dumps on a `PersistentVolumeClaim`. Docker
Compose has no equivalent scheduler, so the manual procedure below still applies there. Copying
dumps off-cluster to durable object storage (S3/GCS/etc.) is explicitly out of scope for this
project — it would need a real cloud storage account, the same class of real-cloud dependency
this project deliberately doesn't take on anywhere else.

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
# Restore into a fresh, empty database — NOT pg_restore -c against the existing "atlas" database.
# See "Why a fresh database, not -c" below for why that matters.
psql -U atlas -d postgres -c "DROP DATABASE IF EXISTS atlas_restored;"
psql -U atlas -d postgres -c "CREATE DATABASE atlas_restored OWNER atlas;"
pg_restore -U atlas -d atlas_restored ./atlas-backup-<date>.dump

# Once verified below, point atlas-api at it: rename databases (or update DATABASE_NAME) and
# restart atlas-api.
```

Verify after restore:

```bash
psql -U atlas -d atlas_restored -c "SELECT count(*) FROM symbols;"   # sanity-check row counts
curl -s http://<atlas-api>/health   # expect {"status":"healthy", ...}
curl -s http://<atlas-api>/prices?symbol=AAPL
```

### Why a fresh database, not `-c`

This runbook originally documented `pg_restore -U atlas -d atlas -c ./backup.dump` — restoring
in place over the existing database with `--clean`. Rehearsing it for real (see "Recovery drill
results" below) found this **does not work reliably against TimescaleDB hypertables**:
`--clean`'s `DROP TABLE`/`ALTER TABLE` sequence conflicts with TimescaleDB's internal chunk
tables (`_timescaledb_internal._hyper_*_chunk`) — Postgres refused most of the DDL
(`operation not supported on chunk tables`, `ONLY option not supported on hypertable
operations`), and pg_dump itself already warns about this on the way out (`circular
foreign-key constraints on ... continuous_agg`). In this repo's case the existing data
happened to survive because most of it was already there and unchanged — but that's luck, not
a property of the command, and in a genuine disaster (a lost/corrupted database) there's no
"already there and unchanged" data to fall back on.

Restoring into a **freshly created, empty** database sidesteps all of it: the dump already
contains `CREATE EXTENSION IF NOT EXISTS timescaledb` plus every internal chunk table
(`_timescaledb_internal._hyper_*_chunk`) and the extension's own catalog rows, so `pg_restore`
recreates the whole hypertable from scratch in one pass instead of trying to reconcile a
`--clean` DROP/ALTER sequence against chunk tables that already exist. This is also the
realistic disaster scenario anyway — a lost volume means starting from nothing, not patching a
live database in place.

### Recovery drill results (2026-08-07, Docker Compose)

Rehearsed for real against the actual dev database (test data only — no real portfolio data
exists yet, so this was safe to run directly rather than against a throwaway copy):

1. Snapshotted row counts and an order-independent MD5 checksum (`md5(string_agg(md5(row::text),
   ''))`) of `transactions`, `positions`, and `prices` before touching anything.
2. Ran the documented backup command — succeeded, produced a 307K dump (34 symbols, 3925 price
   bars, 26 transactions, 5 positions, 207 indicator rows, 160 recommendations, and more).
3. First tried the *old* `pg_restore -c` command from this runbook against the live `atlas`
   database — failed as described above (65 ignored errors). Data (rows) happened to survive
   unchanged (confirmed via the same checksums), but **schema didn't**: the ignored errors turned
   out not to be harmless after all — `prices`' primary key, unique, and foreign-key constraints
   were left silently missing afterward (`\d prices` showed zero constraints), which only
   surfaced later as real test failures (`ON CONFLICT` upserts need the unique constraint to
   target) and as `atlas-api`'s long-running DB connections erroring on stale cached relation
   OIDs for `symbols` (a table the failed restore also touched, though its own schema stayed
   intact — the OID error was a client-side cache problem, not a symbols-table problem). Fixed by
   manually re-adding exactly the constraints `migrations/001`/`002` define
   (`PRIMARY KEY (id, ts)`, `FOREIGN KEY (symbol_id) REFERENCES symbols(id)`,
   `UNIQUE (symbol_id, ts)`, verified first that no duplicate/orphaned rows existed to block
   re-adding them) and restarting `atlas-api` to drop its stale connections. This is the clearest
   argument in this whole drill for never running `pg_restore -c` against a hypertable-containing
   database again — restoring into a fresh database (below) doesn't have this failure mode at all.
4. Restored into a freshly created `atlas_restored` database instead (the corrected procedure
   above) — succeeded with only 2 ignorable errors (a redundant FK constraint attempt against an
   internal chunk table, harmless since TimescaleDB enforces that FK at the hypertable level
   already). Every table's row count matched the original exactly, all three checksums matched
   byte-for-byte, and `prices` came back as a real hypertable
   (`timescaledb_information.hypertables` still lists it), not a degraded plain table.
5. Cleaned up: dropped `atlas_restored`, deleted the dump file (both the in-container `/tmp` copy
   and the host copy) — nothing left behind.

### Recovery drill results (2026-08-07, real Kubernetes — minikube)

Rehearsed against an actual local cluster (`minikube start --driver=docker`, not mocked or
dry-run), since a real cluster was the one piece of infrastructure earlier passes couldn't
provide. `atlas-api`'s image was built directly into minikube's Docker daemon (`eval $(minikube
docker-env) && docker build ...`) so `deploy/deployment.yaml` could run it with
`imagePullPolicy: Never`, no registry needed. TimescaleDB/Redis were deployed as throwaway
test-only Deployments (not part of this repo's manifests — production TimescaleDB/Redis are a
managed service per this file's Scope note; this was purely to have something for `deploy/`'s
real manifests to point at).

This surfaced two real bugs that were invisible in Docker Compose, because Compose doesn't
enforce `runAsNonRoot` the way Kubernetes does:

1. **`Dockerfile`'s `USER appuser`** — Kubernetes' `runAsNonRoot` check can't resolve `/etc/passwd`
   inside the image before starting the container, so it requires the image's own `USER` to
   already be numeric. Every `atlas-api` pod failed immediately (`cannot verify user is
   non-root`, `CreateContainerConfigError`) until `useradd` was given an explicit
   `--uid 10001` and `USER appuser` became `USER 10001`.
2. **`deploy/backup-cronjob.yaml`'s `runAsNonRoot: true`** (with no `runAsUser`) — the official
   `timescale/timescaledb` image has no non-root default user at all (it normally runs as root,
   relying on Postgres's own privilege-drop for the actual server process), so every backup Job
   failed the same way (`image will run as root`). Fixed by adding an explicit `runAsUser: 1000`
   plus `fsGroup: 1000` (needed so the mounted PVC is actually writable by that UID — this
   container only runs `pg_dump`/`find` as a client, never the Postgres server itself, so no
   other image-specific permissions were needed).

With both fixed, the full drill ran clean:

1. Applied `deploy/namespace.yaml`, `configmap.yaml`, a `kubectl create secret` (per
   `deploy/README.md`'s Apply section), `service.yaml`, `pdb.yaml`, `hpa.yaml`, and
   `deployment.yaml` — `atlas-api` came up 2/2 `Running`, `/health` returned `{"status":
   "healthy", "database":"ok","redis":"ok"}` through a real `kubectl port-forward`.
2. Applied `deploy/backup-cronjob.yaml` — the `PersistentVolumeClaim` and `CronJob` both created
   cleanly, correctly scheduled (`0 2 * * *`).
3. Triggered a real run without waiting for 02:00: `kubectl create job --from=cronjob/
   atlas-db-backup atlas-db-backup-manual -n atlas` — completed successfully, produced a real
   `pg_dump` on the PVC, confirmed via the exact `busybox` browse command already documented
   above (which needed no security-context changes itself — plain `busybox` has no non-root
   requirement to satisfy).
4. Copied the dump off the PVC (`kubectl cp`), restored it into a freshly created
   `atlas_restored` database on the cluster's TimescaleDB (the same fresh-database procedure
   verified in the Compose drill) — this dump had no `prices` rows yet (just a test `symbols`
   row), so it restored with zero errors at all, not even the 2 ignorable ones from the Compose
   run. Verified the restored data matched the original exactly.
5. Cleaned up: dropped `atlas_restored`, deleted the manual Job and helper pods, `minikube
   delete` to remove the whole test cluster — nothing left running.

Both fixes are real, permanent corrections to `Dockerfile`/`deploy/backup-cronjob.yaml`, not
drill-specific workarounds — any real deployment to any Kubernetes cluster would have hit them.

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

Docker Compose wires this end to end: `prometheus` loads `alerts.yml` and forwards firing alerts
to the `alertmanager` service (`:9093`), which routes them to a Discord channel — deliberately a
*different* one from `DISCORD_WEBHOOK_URL`'s business alerts, so infra noise never mixes with
investment notifications. Set `ALERTMANAGER_DISCORD_WEBHOOK_URL` in your (gitignored) `.env` —
see `.env.example` — to enable real delivery; leave it empty and Alertmanager still starts and
runs fine, it just logs a failed delivery attempt per alert instead of posting anywhere (the same
non-fatal-optional-dependency pattern as everywhere else in this project).

The webhook URL itself is never written to a file tracked by Git: `alertmanager/alertmanager.yml`
points `discord_configs`' `webhook_url_file` at `/tmp/discord_webhook_url`, which
`docker-compose.yml`'s `alertmanager` service writes from that env var at container start (an
overridden entrypoint, since Alertmanager's config can't read env vars directly, only file
paths).

**Pinned image version matters here**: `prom/alertmanager:v0.27.0` (the version this stack
started on) has a real bug — confirmed live and via its own source on GitHub — where
`discord_configs`' config-load validation only checks `WebhookURL`, never `WebhookURLFile`, so
every `webhook_url_file`-only config fails to load at all (`no discord webhook URL provided`),
regardless of the file's actual content. Fixed in `v0.28.0`; this stack is pinned to `v0.28.1`.

Verified live end to end: config loads cleanly both with `ALERTMANAGER_DISCORD_WEBHOOK_URL` empty
and set; posting a synthetic alert to Alertmanager's API against a fake (but real-shaped) webhook
URL produced a genuine round trip to Discord's actual API (`404 Unknown Webhook`), logged as a
failed delivery, Alertmanager kept running — exactly the intended non-fatal degrade.

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
- [x] Database backup configured — **automated in Kubernetes** (`deploy/backup-cronjob.yaml`: daily `pg_dump`, 7-day retention on a PVC — see the Backup section above); Docker Compose still only has the manual procedure (no scheduler in that stack). Off-cluster/object-storage copy of the dumps is explicitly out of scope for this project (see above).
- [x] Secrets secured — scoped with Author first: manual `kubectl create secret` (already documented in `deploy/README.md`'s Apply section) is the deliberate approach for Kubernetes, not a placeholder awaiting a real secret manager — Sealed Secrets/External Secrets Operator/a cloud secret manager were considered and deliberately not adopted for this project's size. `deploy/secret.yaml` stays a template precisely so it can't be applied as-is by accident. Docker Compose secrets now come from a gitignored `.env` (see `.env.example`) instead of being hardcoded in `docker-compose.yml` — see "Local secrets (Docker Compose)" below.
- [x] Alerts configured — Discord business alerts (`internal/app/alerts_loop.go`) plus infrastructure-level Prometheus alerting rules (`prometheus/alerts.yml`: `AtlasAPIDown`, `AtlasAPIFlapping`, `AtlasPodCrashLooping`, `AtlasHighErrorRate`) routed through Alertmanager to a separate Discord channel — see "Infrastructure alerting" above for how the webhook URL is configured (via `.env`, never committed) and the real `v0.27.0` bug that had to be worked around.
- [x] Recovery tested — backup + restore rehearsed for real on **both** Docker Compose (against the actual dev database, which is how the restore procedure's `-c` bug against TimescaleDB hypertables was caught and fixed) **and a real Kubernetes cluster** (minikube — see "Recovery drill results" above, which is how two real `runAsNonRoot` bugs in `Dockerfile` and `deploy/backup-cronjob.yaml` were caught and fixed). Testing against a real managed/cloud cluster is explicitly out of scope for this project — minikube is real Kubernetes, and that's the level of rehearsal this checklist item asks for here.

This checklist is meant to stay honest, not to look complete — every box above reflects what was
actually verified, not what's merely planned.
