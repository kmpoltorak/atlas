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

**Implementation status**: bar granularity switched from daily (`interval=1d`) to hourly
(`interval=1h`) — daily candles were too coarse for the trading-oriented use this engine targets.
Verified live: Yahoo's unofficial chart endpoint returns clean hourly bars with 1-hour gaps
during market hours, well inside its ~730-day intraday retention limit, so the existing
`COLLECTOR_WINDOW_DAYS` (default 30) needed no change — it now just yields ~150+ hourly bars
instead of ~20 daily ones, comfortably above every indicator's warm-up requirement. Stooq (the
documented free fallback provider) was removed rather than switched to hourly too: its free
endpoint already served a client-side JavaScript bot challenge instead of data on every network
tested, and even where reachable its free CSV export is daily-only, so it couldn't meet the
hourly requirement regardless. Yahoo is now the sole provider; `MARKET_PROVIDER` remains as a
metrics/logging label, not a selector. The `prices`/`indicators` tables have no granularity
column, so old daily history for real symbols (AAPL, MSFT, ALE.WA, SPCX, ETFBDIVPL.WA) was
truncated before the first hourly collection, rather than left to mix silently with new hourly
rows in the same per-symbol series (this would have corrupted `cmd/export-training-data`'s
read-back, the only consumer of stored bar history — nothing in the live recommendation path
reads it back). Scoped with Kris first (truncate vs. leave as-is — truncate was chosen).

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
persist to TimescaleDB); storage of indicator snapshots now is too — every live-recommendation
path (`GET /recommendations`, `GET /alerts`, and the background alert-poll loop, via
`alerts.Alert.Bars`) writes the full computed indicator set (one row per indicator,
migrations/001+006, internal/infrastructure/persistence/indicators.go) to the `indicators` table.
Backtesting deliberately still doesn't — its bars are historical/simulated, not live readings, the
same distinction that already keeps backtesting's individual simulated recommendations out of the
recommendations table (see docs/backtesting.md). Indicators are still computed fresh per request
everywhere; persistence is a side-effect write, not a read/caching path.

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
labeled historical outcomes to define. OBV and Bollinger Bands — real, tested analyzer
computations since an earlier pass but never consumed anywhere — are now blended into
`buildTechnicalScore` too (a Bollinger %B position signal and an OBV accumulation/distribution
signal, weighted alongside momentum/volume and RSI), closing that specific documented gap. This
is a heuristic blend (like the rest of this engine's scoring), not a retrained model — extending
`ml.FeatureVector`/the trained ONNX model to use these indicators too was deliberately not done,
since real accumulated history is still too thin to retrain on new features honestly (same
reasoning as recovery probability, above).

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

Prometheus alerting rules (Alertmanager) for infrastructure-level conditions now exist too,
scoped with Kris first (rules-only in this pass; delivery destination deliberately left a
placeholder — see below): `prometheus/alerts.yml` defines `AtlasAPIDown`, `AtlasAPIFlapping`
(a crash-loop proxy needing no extra scrape config), `AtlasPodCrashLooping` (the literal
Kubernetes crash-loop signal, but inert until kube-state-metrics is scraped, which this repo
doesn't configure — consistent with `deploy/README.md`'s Scope note that cluster-level
add-ons/managed services are out of this repo's scope), and `AtlasHighErrorRate` — distinct
from the existing Discord business-logic alerts in `internal/app/alerts_loop.go`. Wired end to
end in Docker Compose: `prometheus/prometheus.yml` loads the rule file and forwards firing
alerts to a new `alertmanager` service, configured (`alertmanager/alertmanager.yml`) with a
`"null"` receiver — alerts fire and are visible in Alertmanager's UI, but are deliberately not
delivered anywhere yet, since which real destination (Discord/Slack/webhook/email) to wire in is
Kris's call, not a code decision (same reasoning as `secret.yaml`'s placeholder). See
`deploy/RUNBOOK.md`'s "Infrastructure alerting" section for the full list and how to verify it.

Scheduled database backups are now automated in Kubernetes too, scoped with Kris first (daily,
7-day retention): `deploy/backup-cronjob.yaml` adds a `CronJob` that runs `pg_dump` against
TimescaleDB every day at 02:00 and keeps the last 7 days of dumps on a new `PersistentVolumeClaim`
(`atlas-db-backups`), pruning older ones on each run. Docker Compose has no CronJob equivalent, so
its backup procedure is still the manual one in `deploy/RUNBOOK.md`. Deliberately not solved by
this pass: copying dumps off-cluster to durable object storage (S3/GCS/etc.) — which target and
schedule to use for that is an infrastructure/cost decision, not a code one, same reasoning as
everything else still open below.

Not yet done, per `deploy/RUNBOOK.md`'s production checklist (kept deliberately unchecked rather
than marked complete): real secret management wired up (`secret.yaml` is a placeholder template,
not Sealed Secrets/External Secrets/a cloud secret manager), a real Alertmanager receiver (see
above — rules exist, delivery doesn't yet), off-cluster backup storage (see above — the schedule
now exists, but dumps only live on an in-cluster PVC), and an actual end-to-end
recovery-runbook rehearsal against a real deployment. None of these have real design decisions
made yet (which secret manager, which Alertmanager receiver, which object storage) — same
caution as every other "requirements gathered, not built" item in this document.

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

**Removing state, built in the same pass**: `portfolio.UseCase.RemovePosition`/`DELETE
/portfolio`/the dashboard's "Remove" button clear a symbol's *current position* entirely — the
"undo a test entry" need — while leaving the transaction history untouched (an append-only audit
log, matching recommendations/predictions). `watchlist.UseCase.Remove`/`DELETE /watchlist`/the
dashboard's "Remove" button do the equivalent for watchlist symbols. Both verified live via
Docker (recorded, removed, confirmed gone from `GET /portfolio`/`GET /watchlist`).

**Deleting an individual historical transaction, built in a later pass**: `Transaction` gained an
`ID` field (`migrations/003` already had `transactions.id DEFAULT gen_random_uuid()` — nothing
ever read it back). `portfolio.UseCase.RemoveTransaction`/`DELETE /transactions`/the dashboard's
new Transaction History table (below Positions in `portfolio.html`, with its own "Remove" button
per row) delete one transaction and recompute the affected symbol's position from scratch by
replaying its remaining transactions in chronological order (`recomputePosition`/
`applyTransaction`) — the only reliable way to undo a transaction's effect on weighted-average
cost once later transactions have built on top of it; an in-place "subtract this one back out"
can't be reversed cleanly in general. `GetTransactions`/`DeleteTransaction` were also added to
the `Repository` interface and `TimescaleDB`, and `NewUseCaseWithRepository` now hydrates
`Transactions` from storage on startup (it previously only hydrated `Positions` — transaction
history was silently dropped on every restart before this pass, since nothing had ever needed to
read it back). Verified live via Docker: recorded two BUYs (10@100, then 5@200, blending to a
133.33 average cost), deleted the second, and confirmed the position recomputed to exactly
10@100 — as if the second BUY had never happened, not some patched-up approximation.

**Display-language switcher, built in a later pass**: English/Polish, scoped with Kris first
(hand-rolled `map[string]map[Lang]string` dictionary + a `T`/`actionLabel` template func pair,
not a third-party i18n library — consistent with this codebase's stance elsewhere of only
reaching for a dependency when hand-rolling would be inappropriate, e.g. bcrypt/QR encoding but
not TOTP). `internal/app/web/i18n.go` holds ~100 translation keys covering every page (nav,
tables, forms, empty states, JS `confirm()` dialogs via `printf`-style `%s` interpolation for the
transaction-symbol prompts, and a translated `actionLabel` for BUY/SELL/HOLD/ACCUMULATE/REDUCE/
WAIT wherever a raw action value is displayed — kept separate from `actionClass`, which still
matches on the untranslated value). The choice persists in an `atlas_lang` cookie (separate from
the session cookie, survives logout, a year-long `MaxAge` since it's a preference not a session)
set via `POST /dashboard/lang`, exempted from the auth gate like `/static/` so it works
pre-login too. Switching language does a real page reload (a server round-trip to
`/dashboard/lang`, redirecting back via `Referer`), unlike the theme switch, since translated
text is rendered server-side, not toggled client-side. A `TestEveryTranslationKeyHasBothLanguages`
test guards against a key added with only one language silently falling back to English.
Verified live via Docker: every nav label, the portfolio page's transaction history table
(BUY/SELL rendered as KUPNO/SPRZEDAŻ), and a JS `confirm()` dialog with a real interpolated
symbol name ("Usunąć AAPL całkowicie?...") all render correctly server-side in Polish.

**Multi-currency portfolio (PLN/EUR/USD), built in a later pass**: scoped with Kris first
(NBP Table A over Table C — the average/mid reference rate is the standard choice for valuation,
not NBP's own buy/sell cash-exchange spread; conversion shown on every page with a currency-
denominated value, not just Portfolio; any currency allowed per transaction, not one currency
locked per symbol). `internal/domain/currency` defines the three supported codes and
`currency.Base` (PLN) — the currency all internal portfolio math is done in, so the Decision
Engine, risk calc, ROI, and backtesting never need to know multi-currency exists at all (see
PROJECT_CONTEXT.md's Portfolio Engine section). `internal/application/exchangerate` +
`internal/infrastructure/exchangerate` (a real NBP Table A client — `https://api.nbp.pl`, no API
key needed, but requires a non-default User-Agent or it 403s, the same class of issue as the
Yahoo provider's 429 — see PROJECT_CONTEXT.md's Market Data Providers section) fetch and
Redis-cache today's PLN-per-unit rates,
refreshed hourly (NBP itself only republishes once per business day, so this just bounds how
often Atlas calls out, not how fresh the data can be). `Transaction.PriceInPLN` is computed once,
at the moment a transaction is recorded, from whichever rate was current then — never
recomputed later — so a position's weighted-average cost basis only changes when a real
transaction is added or removed, not from FX movement alone (verified: a symbol bought partly in
USD and partly in PLN produces one coherent blended PLN average cost). The dashboard/portfolio
pages show every PLN-denominated value with a small secondary line converting it live to EUR/USD
at today's rate (the `money` template func); the transaction history table instead shows each
transaction's original entered currency plus its recorded (historical, not live-recomputed)
PLN-equivalent, since using today's rate to "convert" a months-old transaction would misrepresent
history. Recording a non-PLN transaction with no exchange-rate source configured is a hard error,
not a silent 1:1 PLN fallback, since that could misrepresent real money. Verified live via
Docker: a real $100 USD purchase converted to 373.24 PLN using the real live NBP rate, and both
English and Polish renders of the Positions/Transaction History tables showing correct
conversions and translated column headers ("Waluta"/"Równowartość w PLN").

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
