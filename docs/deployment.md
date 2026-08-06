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
(migrations/006, internal/infrastructure/persistence/indicators.go), but only from
GET /recommendations — the alert-poll loop's Evaluate() doesn't currently expose per-symbol bars
to persist against, so background snapshots aren't taken; there is no alerts table.
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
Response:
```json
{
 "status":"healthy",
 "database":"ok",
 "redis":"ok"
}
```
Docker healthcheck must verify:
- application availability
- database connectivity
- required dependencies
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
```
There is no database-error counter yet.
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