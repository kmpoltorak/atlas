# Atlas Implementation Plan

## 1. Planning Principle

Implementation must proceed only after architecture and data contracts are approved. This document defines the staged implementation path for Atlas without introducing production code.

## 2. Phase 0 — Architecture Lock

Objectives:
- confirm target deployment topology,
- approve service boundaries,
- confirm database model,
- confirm API contract and response shape.

Exit criteria:
- architecture review signed off,
- ERD approved,
- deployment topology approved.

## 3. Phase 1 — Foundation Services

Scope:
- API shell service,
- health endpoint,
- metrics exporter,
- structured logging setup,
- configuration management through environment variables,
- Docker Compose base environment.

Deliverables:
- `atlas-api`
- `/health`
- `/metrics`
- containerized local stack

## 4. Phase 2 — Data Ingestion

Scope:
- market provider integration,
- symbol ingestion,
- price bar persistence,
- Redis caching layer,
- normalization pipeline.

Deliverables:
- canonical market data schema,
- provider adapter interface,
- collector service with idempotent persistence.

## 5. Phase 3 — Analytics Layer

Scope:
- technical indicator pipeline,
- portfolio state engine,
- watchlist support,
- risk context calculator,
- storage of indicator and portfolio snapshots.

Deliverables:
- indicator service,
- portfolio service,
- explanation-ready input model.

**Implementation status**: indicator service (RSI/EMA/MACD/ATR/Volume/OBV/ADX/Bollinger Bands),
portfolio service, and watchlist support are all real. Risk context calculator
(`portfolio.UseCase.ComputeRisk`) is real and reachable (`GET /portfolio`'s `risk` field) — it
existed with no caller before a prior pass. Storage of portfolio snapshots is done (positions
persist to TimescaleDB); storage of indicator snapshots now is too — `GET /recommendations`
writes the full computed indicator set (one row per indicator, migrations/001+006,
internal/infrastructure/persistence/indicators.go) to the `indicators` table. This only covers
the one live-recommendation request path, not the background alert-poll loop or backtesting —
indicators are still computed fresh per request everywhere, this just also records what was
computed when a symbol is explicitly queried.

## 6. Phase 4 — ML and Decision Engine

Scope:
- feature vector construction,
- model training and inference contract,
- confidence and correction scoring,
- recommendation generation,
- recommendation explanation object.

Deliverables:
- prediction service,
- decision engine,
- recommendation persistence and traceability.

**Implementation status** (see PROJECT_CONTEXT.md and docs/ml.md/decision_engine.md for full
detail): all deliverables above are real. Feature vector construction, the decision engine
(technical + ML + portfolio-risk blend), and recommendation persistence/traceability
(recommendations + predictions both append-only in TimescaleDB) shipped first. Model training
and inference contract shipped after: a real XGBoost correction-probability classifier, trained
on real collected history, exported to ONNX, and loaded in Go (falling back to a deterministic
stub when no model is loaded) — verified live end-to-end through the actual HTTP API and a real
Docker container. ACCUMULATE/REDUCE/WAIT shipped after that, gated on real portfolio position
state and ML confidence, not cosmetic renaming — see docs/decision_engine.md. Not yet done:
recovery-probability/market-condition ML outputs and a separate Risk Engine stage.

## 7. Phase 5 — Observability and Notifications

Scope:
- Prometheus metrics for all major services,
- Grafana dashboards,
- Discord notifications for high-priority actions,
- structured logs in JSON format.

Deliverables:
- observability dashboards,
- alerting rules,
- readiness and health checks.

**Implementation status**: Prometheus metrics, Grafana dashboards, JSON structured logs, and
`/health` are all real. Discord notifications for high-priority actions are now real too —
`isHighPriorityAction` (internal/app/alerts_loop.go) gates the push to BUY/SELL/REDUCE only,
matching docs/decision_engine.md's "Notifications" section; previously every non-HOLD action
(including ACCUMULATE/WAIT) triggered a push.

## 8. Phase 6 — Backtesting and Validation

Scope:
- historical simulation engine,
- walk-forward validation,
- performance and risk metrics,
- protected model/strategy evaluation before promotion.

Deliverables:
- backtest runner,
- result summaries,
- strategy version metadata.

**Implementation status**: the backtest runner (internal/application/backtesting), performance
metrics (Returns/Sharpe/Volatility/Max Drawdown/Win Rate), and result summaries are all real —
see docs/backtesting.md. Two gaps closed in this pass: the historical simulation now judges
ACCUMULATE/REDUCE/WAIT from its own simulated position instead of leaking live portfolio state
(a real, previously-documented bug — see docs/backtesting.md's Implementation status note), and
model/strategy evaluation before promotion is now real — every run scores the loaded ML model's
predictions against realized outcomes (Accuracy/Precision/Recall/F1/ROC AUC) and persists to
`backtest_runs` (migrations/005), queryable via `GET /backtests/history`. Not yet done:
walk-forward train/validate/test date-range splitting (only "no lookahead within one run"
exists) and parameter optimization — both still require a scoping discussion before building,
same caution applied to ACCUMULATE/REDUCE/WAIT and ComputeRisk.

## 9. Phase 7 — Production Readiness

Scope:
- Kubernetes manifests,
- secret handling,
- backup and restore planning,
- deployment automation,
- disaster recovery runbook.

Deliverables:
- `deploy/` structure,
- CI/CD pipeline references,
- recovery checklist.

## 10. Acceptance Criteria

A phase is considered complete only if:

- all services satisfy health and metric requirements,
- the data model is persistent and queryable,
- the recommendation object is explainable,
- backtests are available for strategy validation,
- the architecture is still compliant with the repository’s design constraints.

## 11. Approval Gate

Before any implementation proceeds, the following must be approved:

1. final architecture proposal,
2. database ERD and storage strategy,
3. implementation milestone order.

This plan was already fully approved.
