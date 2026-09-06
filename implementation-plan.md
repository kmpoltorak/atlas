# Atlas Implementation Plan

Every phase below is built. This file is now a status record rather than a forward plan: one
paragraph per phase saying what is real, plus the things that are deliberately **not** built.

It used to be a thousand-line narrative of each individual pass — what was changed, why, and how
it was verified. That detail is better served by the places that own it: `docs/` for how the
system works, `docs/adr/` for the decisions that are expensive to reverse, `CLAUDE.md` for the
rules those decisions produced, and `git log` for the blow-by-blow. Keeping a fourth copy here
only guaranteed it would drift.

## Phase 0 — Architecture Lock

Done. Clean Architecture across four layers, TimescaleDB + Redis, Docker Compose as the primary
target with Kubernetes-ready manifests. See [docs/architecture.md](docs/architecture.md).

## Phase 1 — Foundation Services

Done. `atlas-api` with `/health` and `/metrics`, structured JSON logging (zap), environment-based
configuration, and a containerized local stack.

## Phase 2 — Data Ingestion

Done. Yahoo is the sole provider (Stooq was evaluated and removed — see
[PROJECT_CONTEXT.md](PROJECT_CONTEXT.md) for both findings), with idempotent persistence resting
on a real unique constraint rather than application-level dedup.

Bars are hourly, not daily. One consequence worth recording because it is invisible in the code:
`prices`/`indicators` have no granularity column, so the pre-existing daily history for real
symbols was **truncated** before the first hourly collection rather than left to mix silently with
hourly rows in the same per-symbol series. Mixed granularity would have corrupted
`cmd/export-training-data`'s read-back, which is the only consumer of stored bar history.

## Phase 3 — Analytics Layer

Done. Indicators (RSI/EMA/MACD/ATR/Volume/OBV/ADX/Bollinger Bands), the portfolio engine, the
watchlist, and the risk calculator are all real and reachable. Indicator snapshots are persisted
from every live-recommendation path, and deliberately not from backtesting — its bars are
simulated, not live readings, the same distinction that keeps simulated recommendations out of the
`recommendations` table.

## Phase 4 — ML and Decision Engine

Done. A real XGBoost→ONNX correction-probability model and an independent recovery-probability
model, both optional and hot-swappable from the Account page, with a deterministic stub when
neither is loaded. Market condition is a deterministic ADX+EMA rule, not a trained model. The
score is a four-term Technical+ML+Portfolio+Risk blend.

Promotion is never automatic: `training/retrain.sh` produces a candidate, a human reviews its real
holdout metrics and activates it. See [docs/ml.md](docs/ml.md) for measured performance and
`CLAUDE.md` for the rules that came out of getting this wrong (scale-free features, PR AUC over
ROC AUC, no `scale_pos_weight`).

## Phase 5 — Observability and Notifications

Done. Prometheus metrics on a hand-built registry, provisioned Grafana dashboards, JSON logs, and
`/health` checking real dependencies. Notifications go to Discord, Slack and Telegram in any
combination, gated to the four actionable calls (BUY/ACCUMULATE/REDUCE/SELL).

Infrastructure alerting is separate from business alerting by design, down to a different Discord
webhook, so infra noise never mixes with investment notifications.

## Phase 6 — Backtesting and Validation

Done. The backtest runner, performance and risk metrics, per-run ML evaluation persisted to
`backtest_runs`, and walk-forward train/validate/test splitting with threshold optimization.
Walk-forward results are report-only — nothing auto-promotes into the live engine. See
[docs/backtesting.md](docs/backtesting.md), and [docs/adr/004](docs/adr/004-backtests-execute-at-the-next-open.md)
for why fills happen at the next open.

## Phase 7 — Production Readiness

Done. Kubernetes manifests, a CI pipeline (gofmt/vet/tests against real TimescaleDB+Redis service
containers, `govulncheck`, Docker build validation), scheduled backups as a CronJob, and a
disaster-recovery runbook that has actually been rehearsed — against Docker Compose and against
real Kubernetes (minikube).

Rehearsing it was worth more than writing it: it found that `pg_restore -c` silently breaks
TimescaleDB hypertable constraints, that `USER appuser` fails Kubernetes' `runAsNonRoot` check
where Docker Compose never enforces it, and that Alertmanager v0.27.0 ignores `webhook_url_file`
entirely. All three are recorded in `CLAUDE.md` and `deploy/RUNBOOK.md`.

## Phase 8 — Frontend / UI

Done. A server-rendered `html/template` dashboard — one binary, no separate frontend build —
covering portfolio, recommendations, alerts, backtests, watchlist and account management, styled
with locally vendored Bootstrap and light/dark/auto theming, in six display languages.

Auth is a single admin account from environment variables, opt-in: unset credentials mean no login
gate at all, matching how every other optional dependency behaves. 2FA is TOTP enrolled through a
real in-app QR code and stored durably, not a static env var.

## Not built, on purpose

- **Multi-user accounts and OAuth2/OIDC.** Atlas is permanently single-user
  ([PRODUCT.md](PRODUCT.md)); revisit only if that actually changes.
- **A richer SPA frontend.** Server-rendered templates are the decision, not a stopgap.
- **Automatic model promotion.** A human reviews holdout metrics first — see Phase 4.
- **CI "Push"/"Deploy" stages, off-cluster backup storage, and production-cluster testing.** All
  three need real cloud infrastructure this repo deliberately doesn't take on. minikube is real
  Kubernetes and is the level this project's recovery drill is scoped to.
- **More than one replica.** The lease, fencing epoch, durable cursor and delivery outbox are all
  built and tested individually, but no test yet runs two live processes against one database
  through a handover. See [docs/deployment.md](docs/deployment.md)'s Scaling section, which states
  this rather than claiming otherwise.

## Acceptance criteria

A phase counts as complete only if services satisfy health and metric requirements, the data model
is persistent and queryable, the recommendation object is explainable, backtests are available for
strategy validation, and the architecture still complies with the design constraints in
`CLAUDE.md`.
