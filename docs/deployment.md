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
MARKET_PROVIDER=yahoo
DISCORD_WEBHOOK_URL=
LOG_LEVEL=info
PROMETHEUS_ENABLED=true
```
Secrets must never be stored in Git.
Use:
- .env files locally
- Kubernetes Secrets in production

**Implementation status**: `ADMIN_USERNAME`/`ADMIN_PASSWORD_HASH` (both optional — see
implementation-plan.md's Phase 8 status note) gate the web dashboard and REST API behind a
single-account login when set; generate the hash with `go run ./cmd/generate-admin-credentials`
rather than hand-writing one. Unset, Atlas runs with no login gate at all. 2FA is not an env var
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
isn't configured, since it's a genuinely optional sidecar (`profiles: ["trainer"]` in
`docker-compose.yml`, off by default) — `prometheus`/`alertmanager`/`grafana` default to the
in-network Compose service names instead (`PROMETHEUS_URL`/`ALERTMANAGER_URL`/`GRAFANA_URL`),
since those three have no such profile and are always part of the default stack; set any of them
to an empty string to disable that particular check.

Docker healthcheck must verify:
- application availability
- database connectivity
- required dependencies

`atlas-api`'s own compose healthcheck (`docker-compose.yml`) is `curl -fsS http://localhost:8080/
health`, not `wget` — its runtime base (`debian:bookworm-slim`) doesn't ship `wget`, a real bug
once (`docker inspect` showed a nonstop "unhealthy" status despite `/health` genuinely returning
200) fixed by adding `curl` to the Dockerfile. `atlas-trainer` still uses `wget`, which is fine
there: its image is the full `golang:1.25-bookworm`, which does ship it. See `skills/docker.md`.
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
The application must support horizontal scaling.
Requirements:
- Stateless API
- External database
- External cache
- No local filesystem dependency
Future scaling:
100 instruments:
single instance
1000+ instruments:
multiple analyzer workers
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