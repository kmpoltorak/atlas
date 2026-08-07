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
state and ML confidence, not cosmetic renaming — see docs/decision_engine.md. Risk analysis
(`portfolio.UseCase.ComputeRisk`) is now blended into scoring too (`computeRiskScore`, weight
0.10) — the engine's score is a real four-term Technical+ML+Portfolio+Risk blend, matching
docs/decision_engine.md's "Scoring Model" (there's still no separate pipeline *stage* the way
"Decision Flow" draws it — Risk is computed inline in the Orchestrator, not a standalone
component). Scoped with Kris first (report-only vs. blended into scoring — blended was chosen,
per the original docs/decision_engine.md design). Recovery-probability and market-condition ML
outputs remain not done — real accumulated history is currently far too thin to train them
honestly (1 symbol, 344 bars as of this pass — see docs/ml.md's Implementation status note).

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
see docs/backtesting.md. The historical simulation judges ACCUMULATE/REDUCE/WAIT from its own
simulated position instead of leaking live portfolio state (a previously-documented bug, now
fixed), and model/strategy evaluation before promotion is real — every run scores the loaded ML
model's predictions against realized outcomes (Accuracy/Precision/Recall/F1/ROC AUC) and
persists to `backtest_runs` (migrations/005), queryable via `GET /backtests/history`.
Walk-forward train/validate/test splitting and parameter optimization are now real too, scoped
with Kris first (decision engine thresholds are the thing optimized; a fixed 60/20/20
proportional chronological split, not caller-supplied date ranges; results are report-only,
never auto-promoted to the live engine) — see `GET /backtests/walkforward`
(internal/application/backtesting/walkforward.go) and docs/backtesting.md's Implementation
status note. decision.UseCase's five score-band thresholds are no longer hardcoded package
constants; they're a `decision.Thresholds` field (see internal/domain/decision/types.go),
defaulting to the original hand-picked cutoffs everywhere in live traffic — this is what made
optimization possible without duplicating the decision engine's logic.

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

## 10. Phase 8 — Frontend / UI

**Implementation status**: not started, not previously planned. Atlas today is API-only — every
capability (recording BUY/SELL transactions, managing the watchlist, viewing recommendations,
running backtests) is only reachable via raw HTTP calls to `internal/app`'s handlers (see
docs/deployment.md and the endpoint list in PROJECT_CONTEXT.md). There is no web UI, desktop
app, or CLI client anywhere in this repository, and none of ROADMAP.md/VISION.md ever described
one — VISION.md frames Atlas purely as a "decision support system," not a product with an
interface. Added here as the last phase per explicit request; scope, tech stack, and priority
are not yet decided and need a scoping discussion before any implementation starts, same caution
applied to every other judgment-call phase above.

Scope (tentative, unconfirmed):
- a way to record/edit/delete portfolio transactions (BUY/SELL) without hand-crafting HTTP
  requests — the most immediate gap, since that's currently the only portfolio-write path,
- a way to add/delete symbols to/from a watchlist without hand-crafting HTTP requests,
- a way to view current positions, recommendations, alerts, and backtest results,
- watchlist management,
- modern web UI framework (SwiftUI, React, Vue, Svelte, etc.) with a single-page app architecture,
- authentication and authorization model (OAuth2, OpenID Connect, etc.) for multi-user support but also Open option without auth for single-user deployments.

Deliverables: none yet — pending scoping (framework choice, hosting, auth model, whether this is
a thin API client or something richer).

## 11. Acceptance Criteria

A phase is considered complete only if:

- all services satisfy health and metric requirements,
- the data model is persistent and queryable,
- the recommendation object is explainable,
- backtests are available for strategy validation,
- the architecture is still compliant with the repository’s design constraints.

## 12. Approval Gate

Before any implementation proceeds, the following must be approved:

1. final architecture proposal,
2. database ERD and storage strategy,
3. implementation milestone order.

This plan was already fully approved.
