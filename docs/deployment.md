# Deployment

Atlas runs with Docker Compose, on one machine, for one person. This document describes that
deployment as built; [runbook.md](runbook.md) has the backup, restore and recovery procedures,
and [README.md](../README.md#configuration) every environment variable.

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
an advisory lock, tracked in `schema_migrations`), and shuts down gracefully on `SIGTERM`.

## Install

```bash
git clone <this repo> && cd atlas
cp .env.example .env        # optional: webhooks, passwords; every value has a working default
docker compose up -d
curl -s http://localhost:8080/health
```

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

See [runbook.md](runbook.md): restore the database (a dump into a fresh database, or an
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
`status`, since atlas-api doesn't depend on any of them to serve a request — Docker restarting atlas-api because Grafana is down would fix nothing and break something that was
working. `trainer` is additionally omitted entirely (not even `"error"`) when `TRAINER_URL`
isn't configured, since it's a genuinely optional sidecar (Compose runs it and sets `TRAINER_URL`) — `prometheus`/`alertmanager`/`grafana` default to the in-network Compose service names
instead (`PROMETHEUS_URL`/`ALERTMANAGER_URL`/`GRAFANA_URL`), since all three are in the Compose stack; set any of them
to an empty string to disable that particular check.

`atlas-api`'s own compose healthcheck (`docker-compose.yml`) is `curl -fsS http://localhost:8080/
health`, not `wget` — its runtime base (`debian:bookworm-slim`) doesn't ship `wget`, a real bug
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

`cmd/trainer` (the training sidecar, part of the default Compose stack) exposes its own separate `/metrics` on port 8091, scraped as a second Prometheus job
(`atlas-trainer`, `prometheus/prometheus.yml`) — previously it had no metrics and wasn't scraped
at all, only `/status` (point-in-time JSON, no history) and a best-effort Discord notification on
failure:
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
Everything a second concurrent process would need is built and individually tested — the list
below — but no test runs two live processes against one database through a lease handover, so
running two is not a supported configuration.

## What is done

| Was process-local | Now |
|---|---|
| Portfolio positions/transactions held in memory and written back wholesale | The database is authoritative. Writes go through `persistence.MutatePortfolio` — read, modify and commit in one SQL transaction under `SELECT … FOR UPDATE` — and each request re-reads state via `portfolio.UseCase.ForPortfolio`. A stale snapshot cannot overwrite a newer one. |
| Watchlist held in memory | `watchlist.UseCase.List` reads through to the database, and `add` always attempts the idempotent insert before trusting its local duplicate check. |
| Every process polling and sending its own notifications | A database lease (`app.Leader`, `migrations/018`) means one replica polls at a time. Notification delivery is a durable outbox (`alert_outbox`), so a replica dying mid-send loses nothing. |

## The three multi-process gaps, now closed

1. ~~**Losing the lease does not stop work already running.**~~ Every batch now runs under
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
2. ~~**The notification cursor is not a notification cursor.**~~ `alert_cursor` (migrations/019)
   records what was actually announced, and is written in the same transaction as the outbox
   entry that announced it — neither can exist without the other. It is read fresh at the start
   of every poll rather than seeded once at startup, so a promoted follower inherits the real
   cursor instead of a baseline from before its promotion. The `recommendations` table, which
   ordinary HTTP browsing also writes, no longer influences notification decisions at all.
3. ~~**Active model selection does not propagate.**~~ `refreshRuntimeSettings` now also
   reconciles the active model for both roles against the durable selection
   (`reconcileModelSelection`), so every replica converges on the Settings page's choice within
   the refresh interval rather than only the one that served the click. Activation persists the
   selection *before* swapping, so a failed write changes nothing anywhere and is reported.

**What still keeps this at one process:** the mechanisms above are implemented, and the cases the
follow-up review named are now covered individually — a stale claim's acknowledgement being
refused after a successor completed the same row, shutdown during a send blocked on an
unresponsive channel, and lease loss followed by reacquisition producing a new term and a new
epoch. What still does not exist is a test that runs **two live processes** against one database
through a handover, rather than two workers inside one test binary. Until that exists and passes,
one process is the supported configuration, and this document does
not claim otherwise.

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
