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
reads it back). Scoped with the Author first (truncate vs. leave as-is — truncate was chosen).

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
component). Scoped with the Author first (report-only vs. blended into scoring — blended was chosen,
per the original docs/decision_engine.md design). Recovery-probability and market-condition —
scoped with the Author as "build all three" alongside Risk Engine — turned out to need different
treatment on closer look: recovery probability first got its contract and a real, tested label
definition (`ml.Prediction.RecoveryProbability`, `ml.ComputeRecoveryLabel`) with no trained model
yet, since real accumulated history was far too thin to train one honestly at the time (1 symbol,
344 bars); market condition turned out not to need a trained model at all —
`ml.ClassifyMarketCondition` is a deterministic ADX+EMA technical rule, real and live today
(`Recommendation.MarketCondition`), since regime classification via ADX+EMA is well-established
technical analysis, not something that needs labeled historical outcomes to define. OBV and
Bollinger Bands — real, tested analyzer computations since an earlier pass but never consumed
anywhere — are now blended into `buildTechnicalScore` too (a Bollinger %B position signal and an
OBV accumulation/distribution signal, weighted alongside momentum/volume and RSI), closing that
specific documented gap. This is a heuristic blend (like the rest of this engine's scoring), not
a retrained model — extending `ml.FeatureVector`/the trained correction ONNX model to use these
indicators too was deliberately not done, since retraining *that* model on a new feature layout
still needs more accumulated history than collecting-a-second-independent-model (recovery,
below) does.

**Recovery-probability model, trained in a later pass**: real now, not just contract-ready.
Enough real history had accumulated (8 symbols, hundreds of hourly bars each by this pass) to
revisit the earlier "too thin" call — checked concretely rather than assumed: `cmd/
export-training-data -label-type=recovery` (new flag, reuses the existing correction export's
loop and CSV writer, just swapping which label function computes the target column) produced
222 real usable rows, and `training/train.py --target recovery` (the same training procedure as
the correction model, parametrized rather than duplicated — only the output filename prefix and
metadata's label-definition text differ) trained a real model against them: holdout AUC 0.83,
accuracy 0.65 on n=46. Scoped with the Author first (train on it despite the caveats — same
standard this project already applied to the correction model's own first, imperfect pass — vs.
wait for more data; training was chosen). Honestly, this dataset is thinner than it looks: recovery labels
are only meaningful conditional on a correction happening at all, so 222 rows is a small, skewed
subset of an already-modest correction history, not 222 independent observations, and 3 of 8
symbols have zero recovered examples at all — see docs/ml.md's Implementation status note for
the full caveat. Wired into live inference as a second, fully independent optional model
(`RECOVERY_MODEL_PATH`, `ml.UseCase.RecoveryModel`/`NewUseCaseWithModels` — separate from
`MODEL_PATH`'s correction model; either, both, or neither load independently, matching every
other optional dependency's non-fatal pattern), with its own persisted columns
(`predictions.recovery_probability`/`recovery_model_version` — migrations/010, following
`correction_probability`'s existing NULL-means-not-computed convention exactly). Verified live
end-to-end through the actual HTTP API and a real Docker container: a real prediction for MSFT
returned a genuine, non-default `recovery_probability`, persisted correctly to TimescaleDB.

**Model selection and retraining, built in a later pass**: retraining was manual and promotion
meant editing `MODEL_PATH`/`RECOVERY_MODEL_PATH` and restarting — scoped with the Author as worth
improving. `training/retrain.sh` now automates generating a new, uniquely-versioned candidate
(export + train, both targets) and can be scheduled (cron/systemd timer) to run periodically as
real history accumulates; it never touches which model is live. Promoting a candidate is now a
GUI action instead of an env-var-and-restart one — the Account dashboard page lists every trained
model with its real holdout metrics (`ml.ListModels`, reading each model's metadata sidecar) and
activating one hot-swaps it into live inference immediately via `ml.UseCase.SetModel`/
`SetRecoveryModel` (`atomic.Pointer`, safe under concurrent requests — `Model`/`RecoveryModel`
were plain fields before this, which would have been a data race once swapping was possible), no
restart needed, durably recorded (`migrations/011`) so the choice survives one too. Deliberately
still not automatic end-to-end: a human still picks which candidate goes live, the same
"never auto-promote" stance already applied to walk-forward threshold optimization below — with
training data this thin, a person should see a candidate's real metrics before it drives real
recommendations. Verified live: activated a freshly `retrain.sh`-produced candidate from the
Account page, confirmed the next `/predictions` call returned its version with no restart, then
restarted the container and confirmed the saved selection (not `MODEL_PATH`'s env-var default)
came back up.

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
`isHighPriorityAction` (internal/app/alerts_loop.go) gates the push to the actionable calls only
(BUY/ACCUMULATE/REDUCE/SELL),
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
with the Author first (decision engine thresholds are the thing optimized; a fixed 60/20/20
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
build validation) — the "Push"/"Deploy" stages are explicitly out of scope for this project
(they'd need a real container registry and production cluster credentials, the same class of
real-cloud-infrastructure dependency this repo deliberately doesn't take on anywhere else — see
this section's Recovery drill note on production-cluster testing). CI's pinned action versions were bumped
(`actions/checkout`/`actions/setup-go` v4→v7, `docker/build-push-action` v6→v7) after a run
surfaced a Node 20 deprecation notice — confirmed by checking each action's `action.yml` that the
v4-6 lines still declared `node20` while v7 declares `node24`; no input/behavior changes needed
for how this workflow calls them. Separately, `deploy/configmap.yaml`/`secret.yaml` had drifted
out of sync with `internal/config/config.go`: `MODEL_PATH`/`LIBONNXRUNTIME_PATH` (added with the
ML model work) and `ADMIN_USERNAME`/`ADMIN_PASSWORD_HASH` (added with Phase 8's auth) were never
added to the Kubernetes manifests even though `docker-compose.yml` had them — fixed now, both
optional/non-fatal-when-unset, matching their behavior everywhere else.

Prometheus alerting rules (Alertmanager) for infrastructure-level conditions now exist too,
scoped with the Author first: `prometheus/alerts.yml` defines `AtlasAPIDown`, `AtlasAPIFlapping`
(a crash-loop proxy needing no extra scrape config), `AtlasPodCrashLooping` (the literal
Kubernetes crash-loop signal, but inert until kube-state-metrics is scraped, which this repo
doesn't configure — consistent with `deploy/README.md`'s Scope note that cluster-level
add-ons/managed services are out of this repo's scope), and `AtlasHighErrorRate` — distinct
from the existing Discord business-logic alerts in `internal/app/alerts_loop.go`. Wired end to
end in Docker Compose: `prometheus/prometheus.yml` loads the rule file and forwards firing
alerts to a new `alertmanager` service, which now routes to a real Discord receiver — a
deliberately *separate* channel/webhook from `DISCORD_WEBHOOK_URL`'s business alerts, so infra
noise never mixes with investment notifications, scoped with the Author first. The webhook URL is
never committed: `docker-compose.yml`'s `alertmanager` service writes it to a file from the
gitignored `.env` (see `.env.example`) at container start, and `alertmanager/alertmanager.yml`
points `discord_configs`' `webhook_url_file` at that file. Getting this working live surfaced a
real upstream bug: the originally-pinned `prom/alertmanager:v0.27.0` only validates
`webhook_url` at config-load time, never `webhook_url_file`, so every file-based config failed
to load at all regardless of the file's content — confirmed via Alertmanager's own source,
fixed in `v0.28.0`, this stack is now pinned to `v0.28.1`. Verified live end to end: config
loads cleanly with the webhook empty or set, and a synthetic test alert against a fake webhook
produced a real round trip to Discord's actual API (`404 Unknown Webhook`), logged as a failed
delivery without crashing Alertmanager — the same non-fatal-optional-dependency pattern as
every other external integration in this project. See `deploy/RUNBOOK.md`'s "Infrastructure
alerting" section for the full rule list and verification steps.

Scheduled database backups are now automated in Kubernetes too, scoped with the Author first (daily,
7-day retention): `deploy/backup-cronjob.yaml` adds a `CronJob` that runs `pg_dump` against
TimescaleDB every day at 02:00 and keeps the last 7 days of dumps on a new `PersistentVolumeClaim`
(`atlas-db-backups`), pruning older ones on each run. Docker Compose has no CronJob equivalent, so
its backup procedure is still the manual one in `deploy/RUNBOOK.md`. Copying dumps off-cluster to
durable object storage (S3/GCS/etc.) is explicitly out of scope for this project — it would need
a real cloud storage account this repo doesn't have configured, the same class of real-cloud
dependency this project deliberately doesn't take on anywhere else.

The disaster-recovery runbook has now actually been rehearsed too (Docker Compose), not just
written — see `deploy/RUNBOOK.md`'s "Recovery drill results". This caught a real bug in the
runbook itself: the documented `pg_restore -c` (clean, in-place restore) command doesn't work
reliably against TimescaleDB hypertables — `--clean`'s DROP/ALTER sequence conflicts with
TimescaleDB's internal chunk tables. The corrected procedure restores into a freshly created
database instead, verified byte-for-byte against the original via row-order-independent MD5
checksums across `transactions`/`positions`/`prices`.

Real Kubernetes secret management was scoped with the Author and deliberately resolved as: manual
`kubectl create secret` (already documented in `deploy/README.md`'s Apply section) *is* the
design, not a placeholder awaiting a real secret manager — Sealed Secrets/External Secrets
Operator/a cloud secret manager were considered and rejected as more than this project's size
needs. `deploy/secret.yaml` stays a template so it can't be applied as-is by accident. Docker
Compose secrets moved out of `docker-compose.yml` into a gitignored `.env` (`.env.example`
documents every var with its working default, so `docker compose up` still runs with zero setup).

The recovery drill has now been rehearsed against a real Kubernetes cluster too (minikube — see
`deploy/RUNBOOK.md`'s "Recovery drill results (real Kubernetes)"), closing the "needs real
cluster infrastructure" gap this section previously flagged as blocking. This surfaced two more
real bugs, both fixed: `Dockerfile`'s `USER appuser` (Kubernetes' `runAsNonRoot` check requires a
numeric UID, not a username — every pod failed `CreateContainerConfigError` until it became
`USER 10001`), and `deploy/backup-cronjob.yaml`'s `runAsNonRoot: true` with no `runAsUser` (the
official `timescale/timescaledb` image has no non-root default user at all, so every backup Job
failed the same way until an explicit `runAsUser`/`fsGroup` were added). Docker Compose never
enforces `runAsNonRoot`, so neither bug was visible until tested on real Kubernetes.

Testing against a real managed/cloud production cluster is explicitly out of scope for this
project (scoped with the Author: no production-cluster testing) — minikube is real Kubernetes,
and that's the level of rehearsal this repo's recovery drill is scoped to.

With those three scope calls made, `deploy/RUNBOOK.md`'s production checklist is fully checked
off — every remaining "not yet done" item this section used to list (a real Alertmanager
receiver, off-cluster backup storage, production-cluster recovery testing) is now either built
or explicitly out of scope, not open.

## 10. Phase 8 — Frontend / UI

**Implementation status**: real, scoped with the Author before building (server-rendered Go templates,
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

**Bulk-deleting transactions, built in a later pass**: the Transaction History table's per-row
delete stayed as-is; a checkbox column plus a header "select all" checkbox and a "Delete
selected" button were added around it (`portfolio.UseCase.RemoveTransactions`,
`POST /dashboard/portfolio/transactions/bulk-delete`). Each selected id still goes through
`RemoveTransaction` individually rather than a single batched repository call — it's the
recompute-from-remaining-transactions invariant that matters, not the delete itself, and that
stays trivially correct one-at-a-time even when a batch spans several symbols; errors from
individual ids are joined (`errors.Join`) rather than aborting the whole batch. Both the per-row
and bulk actions live in one `<form>` now (checkboxes can't cross a form boundary): each row's
"Remove" button uses `formaction`/a named submit value to override where *that* click goes,
independent of the bulk button's default action — verified live that both paths still work
correctly from the same page. Verified live via Docker: recorded a BUY+SELL pair, selected both
via checkboxes, bulk-deleted them in one request, confirmed both gone from the transaction table
and the position recomputed to zero — the same recompute semantics as a single delete.

**Bulk-delete extended to every other Remove list, in a later pass**: scoped with the Author as
"bulk-delete should exist everywhere a per-row Remove already does," not just transactions —
Positions (`portfolio.UseCase.RemovePositions`, `POST /dashboard/portfolio/bulk-delete`) and
Watchlist (`watchlist.UseCase.RemoveMany`, `POST /dashboard/watchlist/bulk-delete`) got the
identical checkbox/select-all/formaction-override pattern, and the page-local JS that wires it up
was factored into a shared `atlasSetupBulkDelete` helper (`internal/app/web/static/js/atlas.js`)
so three near-identical inline scripts didn't accumulate across pages. Watchlist's single-row
delete also gained a confirm dialog it was previously missing entirely — a real, if minor, gap
found while touching that template, not a regression. The Account page's per-role ML model
"Remove" was deliberately left as a single-selection dropdown action, not converted to a
checkbox list: it has no enumerable row-per-item UI to attach checkboxes to (see docs/ml.md), so
bulk-delete there would need a real list-UI redesign first, out of scope for this pass. Verified
live via Docker: recorded two positions and two watchlist symbols, bulk-deleted each pair in one
request, confirmed both gone from their respective pages.

**Display-language switcher, built in a later pass**: English/Polish, scoped with the Author first
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

**Multi-currency portfolio (PLN/EUR/USD), built in a later pass**: scoped with the Author first
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

**Post-launch polish pass, built in a later pass**: a full visual/UX audit (the `impeccable`
design skill's critique workflow — dual isolated review, manual + `detect.mjs`) against the
already-shipped Phase 8 dashboard, followed by a self-directed P0-P4 punch list once the
critique's own findings were exhausted. Grouped by theme rather than by PR:

- **Honesty relabeling of computed-but-misleadingly-named fields** — two real findings, both
  fixed by renaming/clarifying rather than by changing the underlying computation (the
  computation was always real, just not what its label implied): `portfolio.RiskSnapshot`'s
  `Volatility`/`Drawdown` are simple current-state heuristics, not the statistical measures of
  the same name `backtesting.Result` genuinely computes — relabeled "Avg. Deviation from Cost"/
  "Unrealized Loss / Exposure" on the dashboard. `Position.CurrentPrice` never refreshes from a
  live market quote at all — it's frozen at whatever the most recent transaction's price was
  (see `applyTransaction`) — relabeled "Last Transaction Price" with an "as of `<date>`" line
  (`priceAsOf`, `web_handlers.go`).
- **Dashboard information density**: a Total P&L `<tfoot>` row on the Positions tables
  (`totalPnL`, `render.go`); an "Active Alerts" summary card (`alertCountsByAction`) so the
  landing page shows whether anything is actionable without a separate visit to `/dashboard/
  alerts`, deliberately not duplicating that page's own persistence side effects; and
  `AlertPollStatus` (`alerts_loop.go`) surfacing the background alert-poll loop's last
  success/failure on the Alerts page itself, not just via the pre-existing `AlertPollErrors`
  Prometheus counter.
- **i18n/timezone/ordering polish**: every `Recommendation.Reason` string (both
  `decision.UseCase`'s and `Orchestrator.blend`'s independent sources) is now translated
  (`reasonLabel`/`joinReasons`) instead of always rendering in English regardless of the
  selected language; every persisted-as-UTC timestamp displays in Europe/Warsaw via a new
  `localTime` template func (`time/tzdata` blank-imported so this doesn't depend on the
  container image having system tzdata); the recommendations/backtests symbol picker and the
  transaction-currency picker sort alphabetically.
- **Correctness/hygiene**: `"/"` being a `net/http.ServeMux` subtree pattern meant every
  unmatched path silently rendered the dashboard with a 200 instead of a 404 — fixed. Page-handler
  failures now render inside Atlas' own chrome (`web.RenderError`/`templates/error.html`) instead
  of net/http's plain-text default, except in `totpQRHandler`, which serves a PNG and correctly
  kept plain-text. A dead `PortfolioPage.Risk` field (computed, passed to the template, never
  once referenced by `portfolio.html`) was removed. A favicon was added — a plain glyph, not a
  designed brand mark (see PRODUCT.md). `migrations/013` added a `schema_migrations` table,
  backfilled through itself, with the convention that every future migration stamps its own
  version — migrations 001-012 shipped with no way to tell what had actually been applied to a
  given database. `internal/infrastructure/persistence`'s test suite writes real
  `TEST...`-prefixed rows into `TEST_DATABASE_DSN` but never cleaned any of it up; `connectTestDB`
  now registers a matching `t.Cleanup` — confirmed live against the real dev database, which had
  23 stale rows accumulated from prior runs before this fix.
- **Client-side table tooling**: every genuine list table (Positions, Transactions, Watchlist,
  Alerts, Backtests Trades — not the key-value Recommendations/Backtests-summary tables) gained a
  text filter, Previous/Next pagination, and a row-count `<tfoot>` where no meaningful numeric
  total already existed, via a new `atlasSetupTableTools` (`atlas.js`). Chosen client-side,
  scoped with the Author first: these tables run at most in the low hundreds of rows for a single-user
  tool, so a real backend query-param/`LIMIT`/`OFFSET` implementation would add backend surface
  area for no benefit at this scale. `atlasSetupBulkDelete` (the three tables that also have
  bulk-delete) was extended to keep its "N selected" count and select-all state correct once rows
  can be hidden by a filter/page change.

Verified live via Docker throughout: isolated template-execution tests for every new render path
(deleted after confirming, not left in the suite), the real dev database before/after states for
both the migration-tracking and test-cleanup fixes, and both Discord webhooks (business +
infra/training) confirmed delivering end-to-end — a direct webhook POST for each, plus a
synthetically-fired Alertmanager alert confirmed via its own `alertmanager_notifications_total`
metric, not just "the URL didn't error."

Not done: multi-user accounts, OAuth2/OIDC, and a richer SPA frontend remain explicitly out of
scope for this increment — revisit only if Atlas's use case actually changes from "personal tool"
to something needing them.

**ML feature-scaling pass, built after a project-wide audit**: a review of the whole repository
for consistency turned up one substantive defect, and it was in the part of the system whose
reported numbers looked healthiest. The ML feature vector fed the analyzer's raw
`ema`/`macd`/`atr` — values in price units — straight into XGBoost. Across the collected universe
EMA spans ~9 (PGE.WA) to ~21,957 (LPP.WA), so a single tree split on it identified the symbol
outright, and per-symbol correction base rates run from 0.00 (GLD, SPY, QQQ, IAU, IBIT, GLDM) to
0.58 (SOXL) — memorising which symbol was in front of it was an excellent strategy, and the model
took it. Two measurements established this rather than argued it: a predictor using **no features
at all**, only each symbol's historical correction rate, beat the trained model 0.757 to 0.707 on
the same holdout; and the model's headline 0.919 accuracy was *worse* than always answering "no
correction" (0.935) on a target where ~6.5% of bars qualify.

What changed:

- **Scale-free features.** `rsi, ema_dist, macd_pct, tr_pct, volume` — ratios and bounded indices
  only. The conversion lives in `ml.BuildFeatureVector`, so training and serving get it
  identically and the analyzer keeps returning price-unit series for `/prices`, the decision
  engine and the `indicators` table, which all legitimately want them. The three renames were
  load-bearing rather than cosmetic: `ml.LoadOnnxModel`'s `feature_order` guard compares names,
  so keeping `ema`/`macd`/`atr` for newly-normalised values would have let every pre-existing
  model load cleanly and silently mis-score. Confirmed live — both previously-active models were
  rejected at startup with an explicit message and the API fell back to the stub, non-fatally.
- **Embargo at the train/holdout seam.** A label looks forward, so ~470 training rows had labels
  computed from bars sitting in the holdout. `train.py` now drops a gap sized to the label
  horizon (10 correction, 30 recovery).
- **Metrics that can't flatter.** Every run reports ROC AUC against a symbol-base-rate baseline,
  PR AUC against the no-skill rate, Brier score with mean-predicted-vs-actual, and accuracy
  against the majority-class baseline — each paired with the number that makes it interpretable.
  A model losing to the baseline triggers a loud warning, `beats_symbol_base_rate: false` in the
  sidecar, and a "DOES NOT BEAT BASELINE" marker in the Account page's picker, which is the exact
  screen where a human decides to promote one.
- **Capacity cut to match the effective sample.** 1,097 positive rows correspond to only ~178
  distinct drop events (one event labels ~10 consecutive bars), so the unregularised 300×depth-4
  fit was fitting noise; now 150×depth-3 with subsampling and L2.
- **No `scale_pos_weight`, deliberately.** The reflex fix for a 1:8 imbalance, and it was
  measured and rejected: it bought ~0.01 ROC AUC (inside the noise) while tripling the Brier
  score, 0.211 vs 0.067, and pushing mean predicted probability to 0.43 against a true 0.065.
  Atlas consumes this output *as* a probability — the decision engine blends
  `1 - correction_probability`, the dashboard shows it to a person — so calibration outranks a
  hundredth of AUC here.

With leakage gone the correction model still trailed the baseline (0.680 vs 0.748), and rather
than accept that, the Author asked for it to work — "nie chcę bezużytecznych rzeczy". Two further
findings closed the gap:

- **Volatility was the missing feature family, and it was missing for a mundane reason.** The
  model's only volatility input was `tr_pct`, the true range of a *single bar*
  (`analyzer.ComputeATR` returns raw per-bar true range despite its name promising a 14-period
  average). Volatility is exactly what separates instruments that correct often from ones that
  never do — SOXL corrected in 58% of windows, GLD in 0% — so it was precisely what the old model
  had been reaching for when it memorised symbol identity. Adding a properly smoothed `atr_pct`
  alone lifted prototype ROC AUC from 0.675 to 0.782; `rvol_20`/`rvol_50` alongside it roughly
  doubled PR AUC. New analyzer methods `ComputeSmoothedATR` and `ComputeRealizedVolatility` back
  them; `ComputeATR` itself was left alone, since three other consumers depend on its current
  meaning. Longer windows were tried and rejected on evidence — most symbols hold ~125 bars, so a
  100-bar window discarded a third of the dataset and made everything worse.
- **The verdict was being judged on the wrong metric.** ROC AUC is dominated by how well the
  vast, uneventful middle is ordered, and Atlas never alerts on the middle. Switching the verdict
  to PR AUC flipped it: the correction model trails on ROC AUC (0.710 vs 0.748) while beating the
  baseline on PR AUC (0.264 vs 0.188). Restricted to its 25 highest-scoring bars it was right 68%
  of the time against a 6.5% base rate — a 10.5x lift, where the same alert budget spent on the
  baseline returned 16%. Judging on ROC AUC would have condemned a model that does the job it is
  deployed to do.

Two structural facts settle why the baseline was never the product regardless of its score. It is
**one constant per symbol** — it ranks instruments by how often they historically fell and can
never say a correction is likelier now than yesterday, which is the entire question Atlas answers
on a 1h timeframe. And it scores a newly-watchlisted ticker at exactly 0.500, having no history
for it, where the model reaches 0.732. A `unseen_symbol_auc` metric was added to every training
run to keep that visible.

Final state: **both models beat the baseline on PR AUC** — correction 0.264 vs 0.188, recovery
0.851 vs 0.651 (recovery also on ROC AUC, 0.758 vs 0.452).

Verified live throughout: two full retrains in the real `atlas-trainer` container against the
real database (10,002 correction / 1,036 recovery rows across 47 symbols), the stale-model
rejection observed in real startup logs, the picker's new "DOES NOT BEAT BASELINE" text read back
off the running dashboard, and real `/predictions` responses after promoting the new models —
including the sanity check that the volatile instruments now score far above the calm ones on
live data (MARA 0.41 and SOXL 0.26 against AAPL 0.042 and MSFT 0.054) — discrimination the
pre-fix model had no honest basis for.

**Watchlist import/export, built after live testing began**: moving a watchlist between
environments meant re-issuing one `curl` per symbol, which for a 47-symbol list is not a workflow
anyone sustains. `GET /watchlist/export` now serves the list as a downloadable JSON file in
exactly the shape `GET /watchlist` already returns, and `POST /watchlist/import` accepts that
same shape back — so an export from one instance imports into another with no conversion step,
which is the whole point. A bare array of tickers (`["AAPL","MSFT"]`) is accepted too, since a
hand-written list is the other realistic way that file gets made. The dashboard's watchlist page
grew a download button and a file-upload form alongside them.

Import defaults to **merge** (add what's missing, touch nothing else — idempotent, so running it
twice is harmless); `mode=replace` additionally untracks whatever the file omits, mirroring it
exactly. Replace is the only path that removes anything and always has to be named explicitly, so
a forgotten parameter can never clear a watchlist. The whole batch is validated before the first
write, because `watchlist.Repository` has no transaction spanning multiple items and a half-
applied import would leave the list in neither the old state nor the new one. Symbols already
tracked keep their original `added_at`; new ones take the file's, falling back to now — so an
export restores as a faithful snapshot rather than restamping everything.

This also fixed a real inconsistency it surfaced: `POST /watchlist` passed symbols through raw
while the dashboard upper-cased and trimmed them, so `{"symbol":"aapl"}` created an entry that
`Add`'s dedup would never match against `AAPL` and the collector would try to fetch verbatim.
Normalisation moved into `watchlist.UseCase.Add`/`Remove`, so every path shares one canonical
form.

Verified live end-to-end on the real 47-symbol watchlist: exported, then deliberately destroyed
the list down to 2 symbols via replace, then restored from the export file and diffed — 47 out,
47 back, symbols and `added_at` identical to the character, persisted through to TimescaleDB and
surviving a container restart. Malformed payloads (junk, wrong object shape, empty symbol,
duplicate symbol, unknown mode) each rejected with a specific message and no partial mutation.

**Live portfolio pricing, built after live testing surfaced how stale it was**:
`Position.CurrentPrice` was set once, at the moment of the trade, and never refreshed —
so a position bought weeks ago still showed that day's price forever, and P&L was silently wrong
for anyone actually checking it. The alert-poll loop's existing per-symbol quote fetch
(`refreshWatchlistQuotes`) was extended to also cover every symbol currently held (not just the
watchlist), and the Dashboard/Portfolio pages now overlay that live quote onto `CurrentPrice` at
render time — `Position.CurrentPrice` itself, and the decision engine's own use of it, were
deliberately left untouched at the Author's explicit call, scoped to display only for this pass.

This surfaced a real bug before it shipped fully: the live quote is in the instrument's own
currency (USD for a US stock), but portfolio math is always PLN — the first version of the
overlay pasted raw USD numbers in where PLN was expected, a 3-4x distortion caught live
(a USD position briefly showing an impossible drawdown). Fixed at the point the quote enters the
system (`quoteToPLN`, converting once via the existing NBP exchange-rate use case), not at every
display call site. Verified live on a real USD position (SPCX): stored quote corrected from raw
130.20/131.28 USD to 485.26/488.88 PLN at the day's real rate, WSE (native-PLN) symbols confirmed
unaffected by the same deploy.

**Portfolio form and data-correctness fixes, built after live use surfaced them**: four small,
independent bugs found by the Author actually using the Portfolio page. The record-transaction
symbol field was free text with no validation — retyped to a dropdown sourced from the watchlist
(matching the pattern Recommendations/Backtests already used), so a typo can't silently create an
untracked position. "Price" was renamed to "Unit Price" everywhere it appears (the form and the
transaction history table) — it was always per-unit, multiplied by quantity downstream, but read
ambiguously as the trade's total cost. A transaction recorded with no explicit timestamp (both the
dashboard form and the JSON API omit it) stored the Go zero time, rendering as `0001-01-01 00:00`
in the UI — `RecordTransaction` now defaults it once, for every caller, rather than relying on a
persistence-layer fallback that never made it back into the in-memory copy the page actually
reads. And selling a position down to exactly zero left a zeroed-out row sitting in the Positions
table requiring a manual "Remove" click — `applyPositionUpdate` now drops it automatically,
transaction history untouched either way; verified live by buying and fully selling SPCX and
watching the position disappear on its own.

**Database backup/restore and a safety-gated Flush, built after the Author asked for a way to
reset Atlas to a clean slate**: the Account page grew three actions. Backup dumps every table
(positions, transactions, watchlist, price/indicator history, recommendations, ML/2FA settings —
everything except `schema_migrations`, which is Atlas's own bookkeeping, not user data) to one
downloadable JSON file; `row_to_json`/`json_agg` do the type encoding inside Postgres itself, so
this needed no per-table Go struct that could drift from the schema. Restore does the reverse via
`json_populate_recordset`, replacing everything inside one transaction. Flush wipes the same
tables back to a freshly-installed state — login is unaffected, since credentials come from
`ADMIN_USERNAME`/`ADMIN_PASSWORD_HASH`, never the database.

Flush is deliberately hard to misuse: a new `backup_log` table (migration 017) tracks when a
backup was last taken, and `FlushAll` refuses outright — not just a disabled button, a real
server-side check a direct `POST` can't bypass — unless one exists **and** is less than five
minutes old (`FlushBackupFreshness`), independent of the alert-poll interval. An old backup no
longer reflects "right before this wipe" once more trades have happened since, so it doesn't
count as a safety net for a wipe happening now. The Account page shows which of the three states
applies (never backed up / backup too old / ready) with a specific explanation for each, and
auto-reloads itself shortly after a Backup download completes — a plain download link never
navigates the browser away, so without that the page kept showing the old locked state even
though the backup had already unlocked it server-side.

Verified live end to end, more than once: Backup → Flush (DB and UI both empty immediately, no
restart needed) → Restore brought back 46 watchlist items, 47 symbols, transactions, 46k+ price
rows, 48k+ indicator rows and 6k+ recommendations exactly matching the pre-flush snapshot. A real
`pg_dump` was taken as an outside safety net before the live Flush test, on top of the app's own
backup. Direct `POST /dashboard/account/flush` confirmed refused (with the specific reason) both
with no backup ever taken and with one older than five minutes.

**Recommendation staleness after a transaction, fixed after live testing surfaced it**: a real bug
report — selling a position to zero left its last-computed recommendation (e.g. "REDUCE") showing
until the next alert-poll cycle recomputed it, up to `ALERT_POLL_INTERVAL_SECONDS` (five minutes)
later, because `BuildRecommendation`'s `HasPosition` gate (BUY vs ACCUMULATE, SELL vs REDUCE) is
only re-evaluated when a recommendation actually gets recomputed. The decision logic itself was
already correct throughout — an on-demand recompute always respected current position state — the
bug was purely in how long the *cached* value stayed stale. Every mutation that can flip
`HasPosition` (recording a transaction, removing a position, deleting a symbol's last transaction)
now triggers an async recompute immediately instead of waiting on the poll loop. Verified live on
SPCX: BUY correctly produced REDUCE and SELL correctly produced HOLD, each within about a second
of the transaction, not five minutes.

**Interactive API docs, built in a later pass**: `swaggo/swag` annotations over every JSON API
handler (portfolio, transactions, watchlist, recommendations, predictions, backtests, prices,
symbols, alerts, health), served at `/swagger/index.html` via `swaggo/http-swagger`. Not in
`publicPaths`, so it needs the same session as the rest of the API when auth is enabled — logging
into the dashboard once lets "Try it out" reuse that session's cookie automatically. The generated
spec (`docs/docs.go`/`swagger.json`/`swagger.yaml`) is committed, the same stance this repo
already takes toward any build artifact `go:embed`/`go build` need on disk (see the vendored
frontend assets); the Dockerfile's builder stage now copies `docs/` alongside `cmd/`/`internal/`.
Verified live: `/swagger/index.html` and `/swagger/doc.json` both 200, all 14 documented paths
present with the correct HTTP methods.

**ML model picker legibility, fixed after live testing surfaced it**: the Account page's model
`<select>` rendered `ModelInfo.Description()` in full as each option's visible text — version,
trained date, PR AUC, ROC AUC, baseline verdict, sample sizes — and a native `<option>` can't wrap
or truncate its own text, so it overflowed the dropdown. The visible label is now
`ModelInfo.ShortLabel()` (version and trained date only); the full metrics move to the option's
`title` attribute and are mirrored into a line below the select on load/change
(`atlasSetupModelDetails`) — nothing lost, since those figures are exactly what the promotion
decision this screen exists for actually turns on, just relocated out of a control that was never
able to display them.

**Risk score misread as "safe" with an empty portfolio, fixed after live testing surfaced it**:
`computeRiskScore` saturates at its neutral 1.0 ceiling both when the portfolio is genuinely
low-risk and when there is simply no exposure to measure (no positions, or every position still at
cost basis) — the two were indistinguishable in the UI, reading as "checked and found safe" when it
actually meant "nothing observed yet." `Recommendation` now carries `RiskDataAvailable` (true only
when `RiskSnapshot.Exposure > 0`), and the Recommendations page shows "n/a" with an explanatory
tooltip instead of the raw score when it's false. Verified live: with a zero-position portfolio,
`GET /recommendations` returned `risk_score: 1, risk_data_available: false`, and the rendered page
showed "n/a" in place of "1.000".

**MIT license and a research/educational-use disclaimer, added on request**: a `LICENSE` file (MIT)
plus a disclaimer in README.md and GETTING_STARTED.md — research/educational purpose, use at your
own risk, Yahoo Finance's unofficial API as the data source. No code changed.

**Slack and Telegram as alternative notification channels, built after Discord had been the only
option**: `internal/infrastructure/notifications` gained a `Notifier` interface (`Send`/
`SendEmbed`) that `DiscordClient`, the new `SlackClient` (Incoming Webhook, `Embed` translated to a
legacy attachment), and the new `TelegramClient` (Bot API `sendMessage`, `Embed` flattened to
Markdown) all implement. `notifications.UseCase` now holds a slice of configured `Notifier`s
instead of a single Discord client and fans every `Send`/`SendEmbed` out to all of them
independently via `errors.Join` — one channel failing never blocks another. Any combination of
`DISCORD_WEBHOOK_URL`/`SLACK_WEBHOOK_URL`/`TELEGRAM_BOT_TOKEN`+`TELEGRAM_CHAT_ID` may be set; all
empty disables notifications exactly as before (alert polling still runs). `alerts_loop.go`'s
gating switched from checking `Config.DiscordWebhookURL` directly to `Notifications.Configured()`,
so it no longer only recognizes Discord. Verified live: `docker compose build/up` clean, `/health`
200, existing Discord-only deployment (`DISCORD_WEBHOOK_URL` set, Slack/Telegram unset) still wires
exactly one notifier with no behavior change. Go toolchain bumped to 1.26.1 in the same pass
(`go.mod`, both Dockerfiles) — unrelated to notifications, just done together.

**Multiple named portfolios, built on request**: up to `portfolio.MaxPortfolios` (5) separate
buckets of positions/transactions — e.g. "Main", "Safe", "Speculative" — switchable via tabs on
the Dashboard/Portfolio pages, managed (create/rename/delete) from a new section on the Account
page. The schema already supported this (`positions`/`transactions.portfolio_id`, `portfolios`
never actually a singleton) — the application layer had just hardcoded a single `"default"` row
(`persistence.defaultPortfolioID`). That's replaced with real portfolio CRUD
(`TimescaleDB.ListPortfolios`/`CreatePortfolio`/`RenamePortfolio`/`DeletePortfolio`, the last
deleting its positions/transactions first in one transaction — no `ON DELETE CASCADE` on those
FKs) and `portfolio.UseCase.ActivePortfolioID`/`SwitchPortfolio`, which reloads Positions/
Transactions from the repository for whichever portfolio is now active — the same mechanism
`Reload()` already used after Flush/Restore, extended rather than replaced.

A deliberate design choice, confirmed with the Author before building: the decision engine treats
"do I already hold this symbol" and "how risky is my overall exposure" as whole-account questions,
not per-portfolio ones — `blendMLAndPortfolio` now reads `GetAllPositions` (every portfolio
combined, quantity-weighted average cost per symbol computed in SQL) instead of the active
portfolio's own `GetPositions`, so a symbol bought in one portfolio correctly reads ACCUMULATE, not
BUY, while a different portfolio's tab happens to be on screen. The Dashboard/Portfolio pages
themselves still show only the active portfolio's own data — the aggregation is internal to
scoring only.

`ponytail`-flagged known limitation: `ActivePortfolioID`/`Positions`/`Transactions` are shared
mutable state on one `UseCase` instance, switched via a cookie read at the top of every relevant
handler (`syncActivePortfolio`) — there is no per-request isolation, so two browser tabs switching
portfolios at the same moment could race. Acceptable for a single-user tool; would need a mutex or
per-request scoping if concurrent multi-tab use ever makes that visible.

Verified live: created a second portfolio via the Account page, confirmed it started empty while
the original portfolio's positions were untouched; bought AAPL in the new portfolio, confirmed
`GET /recommendations?symbol=AAPL` returned ACCUMULATE/REDUCE-family scoring (not BUY) while the
Dashboard's active tab was still the *other*, AAPL-less portfolio, and `risk_data_available: true`
with the correct aggregate exposure; confirmed the dashboard's tabs disappear entirely with only
one portfolio (no inert single tab) and reappear once a second exists; deleted the test portfolio
through the same "manage portfolios" UI and confirmed its position/transaction rows were gone, not
orphaned. Aggregation math and the delete-cascade transaction were also verified directly against
the live TimescaleDB container with dedicated integration tests
(`TestGetAllPositionsAggregatesAcrossPortfolios`, `TestDeletePortfolioAlsoDeletesItsPositionsAndTransactions`).

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
