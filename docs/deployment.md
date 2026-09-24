# Deployment Guide
## Overview
Atlas is designed as a containerized application.
The first deployment target is Docker Compose.
The architecture must be Kubernetes-ready without major redesign.
Deployment environments:
- Development
- Test
- Production
---
# Development Environment
Local development uses:
- Docker Compose
- TimescaleDB
- Redis
- Prometheus
- Grafana
- Atlas API
Example stack:
```
atlas
 |
 +-- atlas-api
 |
 +-- timescaledb
 |
 +-- redis
 |
 +-- prometheus
 |
 +-- grafana
```
---
# Docker Requirements
All services must provide:
- Dockerfile
- Healthcheck
- Environment configuration
- Logging configuration
- Graceful shutdown handling
Rules:
- Use multi-stage builds
- Use minimal images
- Do not run containers as root
- Pin image versions
- Store configuration outside images
---
# Environment Configuration
Configuration must be injected using environment variables.
Example:
```
DATABASE_HOST=timescaledb
DATABASE_PORT=5432
REDIS_HOST=redis
DISCORD_WEBHOOK_URL=
LOG_LEVEL=info
```
Secrets must never be stored in Git.
Use:
- .env files locally
- Kubernetes Secrets in production

**Implementation status**: `ADMIN_USERNAME`/`ADMIN_PASSWORD_HASH` (both optional — see
implementation-plan.md's Phase 8 status note) gate the web dashboard and REST API behind a
single-account login when set; generate the hash with `go run ./cmd/generate-admin-credentials`
rather than hand-writing one. Left unset, Atlas refuses to start unless `AUTH_DISABLED=true` is
also set: running with no login gate is a deliberate local-use choice, not what an incomplete
configuration silently falls back to (`config.Validate`). 2FA is not an env var
at all — it's enabled from the dashboard after logging in (Account → Set up 2FA), via real
QR-code enrollment against a durable database setting (migrations/008).
---
# Docker Compose
Required services:
## atlas-api
Main Go application.
Responsibilities:
- REST API
- Market analysis
- Portfolio engine
- Decision engine
- Discord notifications
- Prometheus metrics
## timescaledb
Stores:
- prices
- indicators
- portfolio
- predictions
- alerts

Implementation status: prices, portfolio (positions, transactions, watchlist), predictions,
recommendations, and backtest runs are all actually persisted. Indicators are now written too
(migrations/006, internal/infrastructure/persistence/indicators.go) from every live-recommendation
path — GET /recommendations, GET /alerts, and the background alert-poll loop (Evaluate() now
carries its per-symbol bars through via alerts.Alert.Bars) — but deliberately not from
backtesting, whose bars are historical/simulated rather than live readings. There is still no
alerts table (alerts are computed on demand from the watchlist, not stored).
## redis
Used for:
- caching
- temporary calculations
- rate limiting
## prometheus
Collects:
- application metrics
- infrastructure metrics
- ML metrics
## grafana
Provides:
- dashboards
- visualization
- alerts
---
# Data Persistence
All stateful services require persistent volumes.
Example:
```
volumes:
timescaledb-data
redis-data
grafana-data
prometheus-data
```
Database data must survive container restart.
---
# Health Checks
Every service must expose health information.
Example:
```
GET /health
```
Real response (`atlas-api`, `internal/app/http_handler.go`'s `healthHandler`):
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
`status`, since atlas-api doesn't depend on any of them to serve a request — Kubernetes/Docker
restarting atlas-api because Grafana is down would fix nothing and break something that was
working. `trainer` is additionally omitted entirely (not even `"error"`) when `TRAINER_URL`
isn't configured, since it's a genuinely optional sidecar (Compose runs it and sets `TRAINER_URL`; Kubernetes has
none) — `prometheus`/`alertmanager`/`grafana` default to the in-network Compose service names
instead (`PROMETHEUS_URL`/`ALERTMANAGER_URL`/`GRAFANA_URL`), since those three are part of every
deployment; set any of them
to an empty string to disable that particular check.

Docker healthcheck must verify:
- application availability
- database connectivity
- required dependencies

`atlas-api`'s own compose healthcheck (`docker-compose.yml`) is `curl -fsS http://localhost:8080/
health`, not `wget` — its runtime base (`debian:bookworm-slim`) doesn't ship `wget`, a real bug
once (`docker inspect` showed a nonstop "unhealthy" status despite `/health` genuinely returning
200) fixed by adding `curl` to the Dockerfile. `atlas-trainer` still uses `wget`, which is fine
there: its image is the full `golang:1.26.8-bookworm`, which does ship it. See `CLAUDE.md`.
---
# Monitoring
Every deployment must expose:
Application metrics:
```
/metrics
```
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

`cmd/trainer` (the training sidecar — part of the default Compose stack, absent on Kubernetes) exposes its own separate `/metrics` on port 8091, scraped as a second Prometheus job
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
`up{job="atlas-trainer"}`
shows as permanently down wherever the trainer isn't running (Kubernetes), same as the
`AtlasPodCrashLooping` alert rule staying dormant on a non-Kubernetes deployment — an inactive
target/rule for an inapplicable environment, not a bug.
---
# Logging
All logs must be:
- JSON formatted
- structured
- timestamped
Example:
```json
{
 "level":"info",
 "service":"collector",
 "ticker":"VOO",
 "message":"price updated"
}
```
Logs should be compatible with:
- Loki
- Elasticsearch
- Cloud logging systems
---
# Kubernetes Deployment
The application must be Kubernetes-ready.
Required manifests:
```
deploy/
namespace.yaml
configmap.yaml
secret.yaml
deployment.yaml
service.yaml
ingress.yaml
hpa.yaml
pdb.yaml
```
---
# Kubernetes Architecture
Recommended setup:
```
Namespace: atlas
Deployment:
atlas-api
StatefulSet:
timescaledb
redis
Services:
atlas-api-service
database-service
redis-service
Ingress:
atlas.example.com
```
---
# Scaling

**Atlas runs one replica. `deploy/deployment.yaml` sets `replicas: 1` with `strategy: Recreate`,
and `deploy/hpa.yaml` pins min and max to 1. Do not raise either yet.**

The API is not stateless, and an earlier version of this document claiming otherwise was wrong.
A later version claiming concurrent replicas were *verified safe* was also wrong, in the opposite
direction — the mechanisms were built, but three gaps were not closed and no two-worker test
existed. Both are the same failure: a deployment claim that the code did not support.

## What is done

| Was process-local | Now |
|---|---|
| Portfolio positions/transactions held in memory and written back wholesale | The database is authoritative. Writes go through `persistence.MutatePortfolio` — read, modify and commit in one SQL transaction under `SELECT … FOR UPDATE` — and each request re-reads state via `portfolio.UseCase.ForPortfolio`. A stale snapshot cannot overwrite a newer one. |
| Watchlist held in memory | `watchlist.UseCase.List` reads through to the database, and `add` always attempts the idempotent insert before trusting its local duplicate check. |
| Every process polling and sending its own notifications | A database lease (`app.Leader`, `migrations/018`) means one replica polls at a time. Notification delivery is a durable outbox (`alert_outbox`), so a replica dying mid-send loses nothing. |

## The three blockers, and where they stand

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
   (`reconcileModelSelection`), so every replica converges on the Account page's choice within
   the refresh interval rather than only the one that served the click. Activation persists the
   selection *before* swapping, so a failed write changes nothing anywhere and is reported.

**What still keeps this at one replica:** the mechanisms above are implemented, and the cases the
follow-up review named are now covered individually — a stale claim's acknowledgement being
refused after a successor completed the same row, shutdown during a send blocked on an
unresponsive channel, and lease loss followed by reacquisition producing a new term and a new
epoch. What still does not exist is a test that runs **two live processes** against one database
through a handover, rather than two workers inside one test binary. Until that exists and passes,
one replica with a non-overlapping rollout is the supported configuration, and this document does
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

Further scaling shape, once the blockers above are closed:
100 instruments:
one instance (today's default)
1000+ instruments:
multiple analyzer workers behind the same lease mechanism
10000+ instruments:
event-driven architecture
Possible technologies:
- NATS
- Kafka
- RabbitMQ
---
# CI/CD
Recommended pipeline:
1. Pull request
Run:
- lint
- unit tests
- integration tests
- security scan
2. Build
Create Docker image.
3. Push
Store image in registry.
4. Deploy
Update environment.
Example:
```
GitHub Actions
      |
Docker Build
      |
Container Registry
      |
Kubernetes Deployment
```
---
# Backup Strategy
Backup:
Database:
daily
Configuration:
Git repository
Models:
versioned storage
Backup targets:
- TimescaleDB dumps
- ML models
- Grafana dashboards
- Prometheus configuration
---
# Disaster Recovery
The system must allow:
- database restore
- application redeploy
- configuration recovery
- model rollback
Recovery priority:
1. Database
2. Configuration
3. Application
4. ML models
---
# Production Checklist
Before production:
[ ] Database backup configured
[ ] Secrets secured
[ ] Metrics available
[ ] Dashboards created
[ ] Alerts configured
[ ] Health checks working
[ ] Logs verified
[ ] Resource limits configured
[ ] Container security checked
[ ] Recovery tested