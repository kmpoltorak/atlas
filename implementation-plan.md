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
per the original docs/decision_engine.md design). Recovery-probability and market-condition —
scoped with Kris as "build all three" alongside Risk Engine — turned out to need different
treatment on closer look: recovery probability got its contract and a real, tested label
definition (`ml.Prediction.RecoveryProbability`, `ml.ComputeRecoveryLabel`) but no trained model,
since real accumulated history is currently far too thin to train one honestly (1 symbol, 344
bars as of this pass — see docs/ml.md's Implementation status note); market condition turned out
not to need a trained model at all — `ml.ClassifyMarketCondition` is a deterministic ADX+EMA
technical rule, real and live today (`Recommendation.MarketCondition`), since regime
classification via ADX+EMA is well-established technical analysis, not something that needs
labeled historical outcomes to define.

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

**Implementation status**: `deploy/` is real — Kubernetes manifests for `atlas-api` (namespace,
configmap, secret template, deployment with non-root/read-only-root security context and
`/health` probes, service, ingress, HPA, PDB — see `deploy/README.md`), plus a disaster-recovery
runbook (`deploy/RUNBOOK.md`) with a manual TimescaleDB backup/restore procedure and an honest
production checklist. `.github/workflows/ci.yml` covers the CI/CD pipeline's "PR" and "Build"
stages (gofmt/vet/test against real TimescaleDB+Redis service containers, `govulncheck`, a Docker
build validation) — the "Push"/"Deploy" stages are deliberately not implemented, since they need
a real container registry and cluster credentials this repo doesn't have configured (see
`deploy/README.md`'s "Not yet done"). CI's pinned action versions were bumped
(`actions/checkout`/`actions/setup-go` v4→v7, `docker/build-push-action` v6→v7) after a run
surfaced a Node 20 deprecation notice — confirmed by checking each action's `action.yml` that the
v4-6 lines still declared `node20` while v7 declares `node24`; no input/behavior changes needed
for how this workflow calls them. Separately, `deploy/configmap.yaml`/`secret.yaml` had drifted
out of sync with `internal/config/config.go`: `MODEL_PATH`/`LIBONNXRUNTIME_PATH` (added with the
ML model work) and `ADMIN_USERNAME`/`ADMIN_PASSWORD_HASH` (added with Phase 8's auth) were never
added to the Kubernetes manifests even though `docker-compose.yml` had them — fixed now, both
optional/non-fatal-when-unset, matching their behavior everywhere else.

Not yet done, per `deploy/RUNBOOK.md`'s production checklist (kept deliberately unchecked rather
than marked complete): scheduled/automated database backups (today's procedure is manual,
run-it-yourself `pg_dump`), real secret management wired up (`secret.yaml` is a placeholder
template, not Sealed Secrets/External Secrets/a cloud secret manager), Prometheus alerting rules
(Alertmanager) for infrastructure-level conditions (pod crash-looping, high error rate — distinct
from the existing Discord business-logic alerts in `internal/app/alerts_loop.go`), and an actual
end-to-end recovery-runbook rehearsal against a real deployment. None of these have real design
decisions made yet (backup schedule/retention, which secret manager, Alertmanager receiver) —
same caution as every other "requirements gathered, not built" item in this document.

## 10. Phase 8 — Frontend / UI

**Implementation status**: real, scoped with Kris before building (server-rendered Go templates,
full dashboard scope, single account + password + TOTP). Every capability listed below as
"tentative" is now reachable through a browser, not just raw HTTP: recording BUY/SELL
transactions (`GET`/`POST /dashboard/portfolio`), viewing positions/risk, running recommendations
(`/dashboard/recommendations`), viewing alerts (`/dashboard/alerts`), running backtests
(`/dashboard/backtests`), and adding watchlist symbols (`/dashboard/watchlist`) — see
`internal/app/web_handlers.go` and `internal/app/web/`.

Concrete decisions made (deliberately narrower than the broader options this section originally
listed as open — SPA frameworks, OAuth2/OpenID Connect multi-user support — which weren't chosen
for this increment, consistent with Atlas being a single-user personal tool, not a
multi-tenant product):
- **Stack**: server-rendered `html/template`, embedded via `go:embed` — one binary, no separate
  frontend build/deploy, no JS framework dependency. Styled with Bootstrap 5.3, vendored locally
  under `internal/app/web/static/` (not loaded from a CDN — offline-friendly, and consistent with
  "no separate frontend build": these are plain files, not an npm/webpack pipeline) and served via
  `/static/`. Light/dark/auto theme switching uses Bootstrap's native `data-bs-theme` attribute
  plus a small inline script (`prefers-color-scheme` + `localStorage`, applied before first paint
  to avoid a flash of the wrong theme) — verified with real Chrome headless screenshots at both
  desktop and mobile widths, light and (forced) dark.
- **Auth model**: a single admin account configured via env vars (`ADMIN_USERNAME`,
  `ADMIN_PASSWORD_HASH` — see `cmd/generate-admin-credentials`), not a `users` table — no
  registration flow, matching "personal tool," not "product with accounts." Auth is opt-in: unset
  credentials mean **no login gate at all**, the same non-fatal-optional pattern as
  `MODEL_PATH`/`DISCORD_WEBHOOK_URL` — every existing curl-based workflow and test this project
  already relies on keeps working unchanged.
- **2FA**: TOTP (RFC 6238) implemented directly on stdlib `crypto/hmac`/`crypto/sha1`
  (`internal/application/auth/totp.go`) rather than a third-party library — verified bit-exact
  against RFC 6238 Appendix B's official test vectors. Password hashing uses
  `golang.org/x/crypto/bcrypt` (hand-rolling password hashing would be inappropriate, unlike
  TOTP's well-specified HMAC construction). **Revised after initial build**: 2FA is not a static
  env-var secret — it's enabled/disabled from the dashboard's Account page after logging in, via
  real QR-code enrollment (Account → Set up 2FA → scan with an authenticator app → confirm with a
  live code), durably stored in TimescaleDB (`migrations/008`'s `auth_settings` table), not just
  displayed once at the command line. QR rendering uses `github.com/skip2/go-qrcode` (the other
  new runtime dependency — hand-rolling QR encoding's matrix/error-correction algorithm would be
  inappropriate, unlike TOTP). Verified live end-to-end: real QR code decoded with `zbarimg`
  (actually scannable, not just an image that renders), the decoded secret used to compute a real
  code and complete enrollment, full login with that 2FA enabled, and disabling 2FA (which
  requires re-entering the current password).
- **Sessions**: opaque tokens in Redis (already part of the stack) with a 24h TTL — no new
  infrastructure, no signed-cookie state to get subtly wrong. Pending TOTP enrollment secrets use
  the same store with a separate 10-minute-TTL key namespace.
- **Gating**: when auth is enabled, every route except `/health`, `/metrics`, `/login`, and
  `/static/*` requires a valid session — `/dashboard/*` (browser-facing) redirects to `/login`;
  the raw JSON API gets a real `401` instead, so an API consumer never gets redirected into an
  HTML login page.
- **Symbol pickers**: Recommendations and Backtests offer the watchlist as a `<select>` dropdown
  instead of a free-text field when the watchlist isn't empty (falls back to text input
  otherwise) — see `web.RecommendationsPage.Symbols`/`BacktestsPage.Symbols`.

Verified live via Docker: full password+TOTP login flow (correct/wrong password, correct/wrong
TOTP code, uniform error message either way), session gating on both dashboard and API paths,
recording a real transaction and watchlist entry through the HTML forms, viewing real
recommendations/alerts/backtest results, the full QR-code 2FA enrollment/login/disable cycle, and
the Bootstrap redesign (light/dark, desktop/mobile) via real browser screenshots — all against
the live TimescaleDB/Redis stack, not mocked.

**Removing state, built in the same pass**: rather than deleting an individual historical
transaction (which would need a transaction `id` — the domain type doesn't have one yet — and a
real decision on how to recompute the affected position's average cost/quantity afterward),
`portfolio.UseCase.RemovePosition`/`DELETE /portfolio`/the dashboard's "Remove" button clear a
symbol's *current position* entirely — the actual "undo a test entry" need — while leaving the
transaction history untouched (an append-only audit log, matching recommendations/predictions).
`watchlist.UseCase.Remove`/`DELETE /watchlist`/the dashboard's "Remove" button do the equivalent
for watchlist symbols. Both verified live via Docker (recorded, removed, confirmed gone from
`GET /portfolio`/`GET /watchlist`).

**Requirements gathered for a follow-up pass (not yet built — real design decisions needed
first, same caution as everything else in this document)**:
- Deleting an individual historical transaction (not just clearing the current position) — still
  needs a transaction `id` and a decision on recomputation strategy, as above.
- A display-language switcher (Polish/English at minimum) — needs an i18n mechanism (every
  template string routed through a lookup keyed by language, a way to persist the chosen
  language) that doesn't exist anywhere in this codebase yet; a meaningfully larger addition than
  the others.

Not done: multi-user accounts, OAuth2/OIDC, and a richer SPA frontend remain explicitly out of
scope for this increment — revisit only if Atlas's use case actually changes from "personal tool"
to something needing them.

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
