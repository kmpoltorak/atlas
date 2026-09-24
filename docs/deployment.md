# Deployment

Atlas runs with Docker Compose, on one machine, for one person. This document describes that
deployment as built, including every environment variable; [runbook.md](runbook.md) has the
backup, restore and recovery procedures.

## What runs, and which versions

`docker compose up -d` starts every service; `docker-compose.yml` is the complete list.

| Service | Image | Notes |
|---|---|---|
| `atlas-api` | built from `Dockerfile` (Go 1.27.1 builder, `debian:bookworm-slim` runtime, ONNX Runtime 1.28.0) | `linux/amd64`; `linux/arm64` builds and runs, model inference unverified on real ARM (docs/ml.md) |
| `atlas-trainer` | built from `training/Dockerfile` (`golang:1.27.1-bookworm` + Python 3.11) | runs retrain.sh on demand for the Settings page |
| `timescaledb` | `timescale/timescaledb:2.29.1-pg16` | pinned: the extension version lives in the data volume |
| `redis` | `redis:7.4.10-alpine` | bars and exchange-rate cache |
| `prometheus` | `prom/prometheus:v3.0.0` | |
| `alertmanager` | `prom/alertmanager:v0.28.1` | not below v0.28.0: v0.27.0 ignores `webhook_url_file`, see the comment in `docker-compose.yml` |
| `grafana` | `grafana/grafana:11.4.0` | |

Every published port is bound to `127.0.0.1`. **Atlas has no login**: anyone who can reach port
8080 can read and change everything, so the stack must not be exposed beyond the machine. Only
cross-origin protection remains, which stops a web page in the same browser from posting to the
dashboard.

`atlas-api` runs as the numeric user 10001, applies its own database migrations on start (under
an advisory lock, tracked in `schema_migrations`), and shuts down gracefully on `SIGTERM`. The
`./migrations` mount in `docker-compose.yml` only pre-seeds a brand-new volume.

The trainer publishes no host port: atlas-api calls its `/train`, `/status` and `/delete`
endpoints through `TRAINER_URL`, which backs the Settings page's "Train now" and model "Remove"
buttons ([training/README.md](../training/README.md)).

**What persists.** Named volumes (`timescaledb-data`, `redis-data`, `prometheus-data`,
`alertmanager-data`, `grafana-data`, `trainer-gomod-cache`) survive `docker compose down` and are
destroyed by `docker compose down -v`. Trained models are a bind mount instead:
`./training/models`, read-only into atlas-api and read-write into the trainer, which is how a
model the trainer writes becomes selectable on the Settings page without a rebuild.

## Install

```bash
git clone <this repo> && cd atlas
cp .env.example .env        # optional: webhooks, passwords; every value has a working default
docker compose up -d
curl -s http://localhost:8080/health
```

## Configuration

Every setting is an environment variable (`internal/config/config.go`) with a working default,
and every optional integration is non-fatal when unset. Put overrides in `.env`
(`.env.example` has the common ones with comments; `.env` is gitignored).

| Variable | Default | Purpose |
|---|---|---|
| `SERVER_PORT` | `8080` | HTTP listen port |
| `LOG_LEVEL` | `info` | zap log level |
| `DATABASE_HOST`/`PORT`/`NAME`/`USER`/`PASSWORD` | `timescaledb`/`5432`/`atlas`/`atlas`/`atlas` | TimescaleDB connection; `DATABASE_PASSWORD` also sets the `timescaledb` container's password |
| `DATABASE_SSLMODE` | `disable` | libpq SSL mode — `disable` matches the Compose Postgres; `require` or `verify-full` for one that serves TLS |
| `REDIS_HOST`/`PORT` | `redis`/`6379` | Redis connection |
| `REDIS_PASSWORD` | *(empty → no auth)* | Redis `AUTH` password; the Compose `redis` service uses the same variable as its `requirepass` |
| `REDIS_TLS` | `false` | TLS 1.2+ to a Redis that serves it |
| `COLLECTOR_WINDOW_DAYS` | `30` | History requested per collection call |
| `ALERT_POLL_INTERVAL_SECONDS` | `300` | Background watchlist-evaluation interval |
| `DERIVED_RETENTION_DAYS` | `0` *(keep everything)* | Days of indicators, recommendations and predictions to keep, pruned daily. The poll writes one recommendation per symbol per cycle, so set this on a long-lived instance |
| `DISCORD_WEBHOOK_URL` | *(empty → off)* | Business alerts (BUY/ACCUMULATE/REDUCE/SELL, on change) |
| `SLACK_WEBHOOK_URL` | *(empty → off)* | The same alerts to a Slack Incoming Webhook |
| `TELEGRAM_BOT_TOKEN` / `TELEGRAM_CHAT_ID` | *(empty → off)* | The same alerts via a Telegram bot; both must be set |
| `ALERTMANAGER_DISCORD_WEBHOOK_URL` | *(empty → off)* | Infra alerts and failed training runs, deliberately a separate channel |
| `ML_ENABLED` | `true` | Initial state of the ML factor; the Settings page toggle overrides it once used. `false` drops only the ML factor, not portfolio risk |
| `MODEL_PATH` | *(empty → stub)* | Correction model `.onnx`; a Settings-page selection overrides it |
| `RECOVERY_MODEL_PATH` | *(empty → 0)* | Recovery model `.onnx`, independent of `MODEL_PATH` |
| `ML_MODELS_DIR` | `/models` | Where the Settings page looks for trained models |
| `LIBONNXRUNTIME_PATH` | `/usr/lib/libonnxruntime.so` | Native ONNX Runtime library |
| `TRAINER_URL` | Compose: `http://atlas-trainer:8091` | The trainer; empty hides the training buttons, and the page explains running `training/retrain.sh` by hand |
| `PROMETHEUS_URL`/`ALERTMANAGER_URL`/`GRAFANA_URL` | the Compose service URLs | Checked by `/health`; set one empty to disable that check |
| `GRAFANA_ADMIN_PASSWORD` | `admin` | Read by the `grafana` container only |

## Update

```bash
# 1. Back up first: Settings page → Backup, or a pg_dump (runbook.md).
git pull
docker compose up -d --build
curl -s http://localhost:8080/health
```

Migrations run on start. A backup taken before an upgrade still restores after it: the
Settings-page restore reads every earlier backup format, and columns added since take their
defaults. Changing a pinned image version (above) is a deliberate step, never a side effect of a
pull; back up before bumping TimescaleDB in particular.

## Recover

See [runbook.md](runbook.md): restore the database (a dump into a fresh database, or a
Settings-page backup), then `git checkout` the configuration, then `docker compose up -d`.

## Health checks

`GET /health`. Real response (`atlas-api`, `internal/app/http_handler.go`'s `healthHandler`):
```json
{
 "status":"healthy",
 "service":"atlas-api",
 "database":"ok",
 "redis":"ok",
 "trainer":"ok",
 "prometheus":"ok",
 "alertmanager":"ok",
 "grafana":"ok"
}
```
Only `database`/`redis` affect `status`/the HTTP status code (503 when either fails) — atlas-api
genuinely can't function without them. `trainer`/`prometheus`/`alertmanager`/`grafana` are purely
informational (`pingHTTPStatus`): reported when reachable, `"error"` when not, but never flip
`status`, since atlas-api doesn't depend on any of them to serve a request — Docker restarting
atlas-api because Grafana is down would fix nothing and break something that was working.
`trainer` is omitted entirely (not even `"error"`) when `TRAINER_URL` is empty. The other three
default to the in-network Compose service names (`PROMETHEUS_URL`/`ALERTMANAGER_URL`/
`GRAFANA_URL`); set any of them to an empty string to disable that check.

`atlas-api`'s own compose healthcheck (`docker-compose.yml`) is
`curl -fsS http://localhost:8080/health`, not `wget` — its runtime base (`debian:bookworm-slim`) doesn't ship `wget`, a real bug
once (`docker inspect` showed a nonstop "unhealthy" status despite `/health` genuinely returning
200) fixed by adding `curl` to the Dockerfile. `atlas-trainer` still uses `wget`, which is fine
there: its image is the full `golang:1.27.1-bookworm`, which does ship it.

## Monitoring

Actual metric names (internal/observability/metrics.go):
```
atlas_api_requests_total
atlas_request_duration_seconds
atlas_provider_requests_total
atlas_recommendations_total
atlas_predictions_total
atlas_alerts_sent_total
atlas_alert_poll_errors_total
atlas_dependency_up{dependency="database|redis|trainer|prometheus|alertmanager|grafana"}
```
`atlas_dependency_up` is a gauge (1 up / 0 down), kept fresh every 15s by
`StartDependencyStatusPolling` — the same checks `/health` itself runs (see
`Application.dependencyStatuses`), just graphable over time instead of only a point-in-time JSON
response. `atlas-api`'s own liveness is Prometheus' built-in `up{job="atlas-api"}` instead — it
can't self-report the way the others can (if atlas-api itself is down, no gauge push happens
either). Both are visualized together on Grafana's "Atlas Overview" dashboard's "Service Status"
panel (`grafana/dashboards/overview.json`).

`cmd/trainer` (the training sidecar) exposes its own `/metrics` on port 8091, scraped as a second
Prometheus job (`atlas-trainer`, `prometheus/prometheus.yml`):
```
atlas_trainer_runs_total{outcome="success|failure"}
atlas_trainer_run_duration_seconds
atlas_trainer_last_run_timestamp_seconds
```
`atlas_trainer_last_run_timestamp_seconds` is what the "Time Since Last Training Run" Grafana
panel is built on (`time() - atlas_trainer_last_run_timestamp_seconds`, formatted with Grafana's
`dtdhms` (days/hours/minutes/seconds) duration unit — plain days read badly for a run that just
finished, e.g. `0.0003 d`) — a way to notice retraining has gone stale without anyone
remembering to check.
`up{job="atlas-trainer"}` reads as down only while the trainer container is stopped.

## Logging

Structured JSON via zap on stdout (`docker compose logs atlas-api`), with typed fields rather than
formatted strings.

## One `atlas-api` at a time

Atlas is supported with exactly one `atlas-api` process per database, which is what Compose runs.
An overlapping restart can still briefly run two, and the state that matters is built not to
depend on there being one:

| Was process-local | Now |
|---|---|
| Portfolio positions/transactions held in memory and written back wholesale | The database is authoritative. Writes go through `persistence.MutatePortfolio` — read, modify and commit in one SQL transaction under `SELECT … FOR UPDATE` — and each request re-reads state via `portfolio.UseCase.ForPortfolio`. A stale snapshot cannot overwrite a newer one. |
| Watchlist held in memory | `watchlist.UseCase.List` reads through to the database, and `add` always attempts the idempotent insert before trusting its local duplicate check. |
| Every process polling and sending its own notifications | A database lease (`app.Leader`, `migrations/018`) means one process polls at a time. Notification delivery is a durable outbox (`alert_outbox`), so a process dying mid-send loses nothing. |

Three gaps a second process would expose were closed on top of that:

1. **Losing the lease stops work already running.** Every batch now runs under
   `Leader.Term()`, a context cancelled the moment leadership is lost or shutdown begins, and it
   is checked between symbols rather than once at the start. Shutdown cancels the term, joins the
   renewal goroutine, and only then releases the lease. Writes are additionally fenced on
   `holder`+`epoch` (`persistence.requireLeadership`): a superseded process's write fails rather
   than landing late, because the check takes the lease row's lock inside the same transaction as
   the write it guards. Outbox rows are claimed with `FOR UPDATE SKIP LOCKED`, so two workers
   cannot deliver the same entry, and an acknowledgement from a lapsed claim is refused rather
   than applied (see "Delivery guarantee"). The term and its fencing epoch are taken as one
   snapshot per batch, so a batch cannot begin under one lease and write under the next one's
   token. Shutdown cancels the term, **joins the polling worker**, and only then releases —
   cancelling aborts in-flight I/O, but "aborted" is not "returned". Cancellation reaches the
   notification clients themselves (`Notifier.SendEmbed` takes a context), the watchlist
   evaluation loop, the quote refresh, and now the market provider itself
   (`market.MarketProvider`'s methods take a context, so a fetch already in flight ends with the
   term rather than running on to the HTTP client's own timeout). Leadership is also no longer a
   flag the renewal loop flips: the lease carries an expiry, `Leader.IsLeader` refuses a lease
   whose deadline has passed, and a watchdog stands the process down if a renewal never lands —
   so a stalled renewal loop cannot leave singleton work running under an expired term.
2. **The notification cursor records what was announced.** `alert_cursor` (migrations/019)
   records what was actually announced, and is written in the same transaction as the outbox
   entry that announced it — neither can exist without the other. It is read fresh at the start
   of every poll rather than seeded once at startup, so a promoted follower inherits the real
   cursor instead of a baseline from before its promotion. The `recommendations` table, which
   ordinary HTTP browsing also writes, no longer influences notification decisions at all.
3. **Active model selection propagates.** `refreshRuntimeSettings` also reconciles the active
   model for both roles against the durable selection (`reconcileModelSelection`), so every
   process converges on the Settings page's choice within the refresh interval rather than only
   the one that served the click. Activation persists the
   selection *before* swapping, so a failed write changes nothing anywhere and is reported.

Each of these is tested individually — a stale claim's acknowledgement refused after a successor
completed the same row, shutdown during a send blocked on an unresponsive channel, lease loss
followed by reacquisition producing a new term and epoch. No test runs **two live processes**
against one database through a handover, so running two on purpose is not supported.

## Delivery guarantee

Alert delivery is **at-least-once per channel**, not exactly-once. The outbox records success
after the send returns, so a crash or database failure between those two points causes a retry
that sends again. What the outbox does guarantee is that nothing is silently dropped, and that a
retry does not fan back out to channels already recorded as delivered.

"At-least-once" is a statement about duplicate *sends*. It is not a licence for delivery state to
move backwards, and three mechanisms keep it from doing so:

- **A claim is a lease with a generation.** Claiming an entry takes it for two minutes and bumps
  its `claim_seq`. Every acknowledgement names the generation it belongs to, so a worker whose
  claim lapsed mid-send cannot clear the successor's claim, restore channels the successor already
  delivered to, or overwrite its result. The refusal is a `WHERE claim_seq = ...` in the update,
  not a check in Go.
- **A batch stops when its claims lapse.** Fifty entries are claimed at once and sent one at a
  time; the loop checks the claim deadline before each send and stops rather than sending on a
  lease it no longer holds. The remaining rows are already claimable, so nothing is stranded.
- **Delivery is oldest-first.** The `ORDER BY` inside the claim's subquery chooses which rows are
  taken and says nothing about the order `RETURNING` emits them in, so the order is applied again
  after the claim.

What is still admitted: the send happens before its success is recorded, so a process that dies in
between causes one duplicate notification on the retry. That window is bounded by the claim TTL.

## CI

`.github/workflows/ci.yml` runs gofmt, vet, the full test suite against real TimescaleDB and Redis
service containers and a real ONNX Runtime, `govulncheck`, the Python tests, and a Docker build,
on every pull request. There is no push or deploy stage: Atlas runs where it is built.
