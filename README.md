# Atlas — AI Portfolio Advisor

Atlas is an AI-powered **decision-support** platform for a personal stock/ETF portfolio. It
watches a set of symbols, computes technical indicators, runs ML predictions, and blends them
with your actual portfolio state into an explainable BUY/ACCUMULATE/HOLD/REDUCE/SELL/WAIT
recommendation — with a confidence score and a human-readable reason every time.

**Atlas never places trades.** It has no brokerage integration and no execution path. It is an
analytical assistant, not a trading bot.

> **Disclaimer.** Atlas was built for research and educational purposes. It is not financial
> advice, and using it — or acting on anything it recommends — is entirely at your own risk. Market
> data comes from Yahoo Finance's unofficial, unsupported API, which can be delayed, incomplete, or
> wrong. This project is open source under the [MIT License](LICENSE).

**New to Atlas and just want to use the dashboard?** See
[GETTING_STARTED.md](GETTING_STARTED.md) — a plain-language walkthrough with no technical
background assumed. Everything below this point is the technical/developer README.

This README gives a full tour of what the project actually does today. [docs/](docs/) is the
authoritative detail and this README is a map, not a duplicate;
[implementation-plan.md](implementation-plan.md) says what is built and what is deliberately not,
[PROJECT_CONTEXT.md](PROJECT_CONTEXT.md) the scope and hard constraints, and
[CLAUDE.md](CLAUDE.md) the rules the code is held to.

## Table of Contents

- [What it does](#what-it-does)
- [Architecture](#architecture)
- [Tech stack](#tech-stack)
- [Quickstart (Docker Compose)](#quickstart-docker-compose)
- [Configuration](#configuration)
- [Web dashboard](#web-dashboard)
- [REST API](#rest-api)
- [Machine learning](#machine-learning)
- [Backtesting](#backtesting)
- [Observability](#observability)
- [Kubernetes deployment](#kubernetes-deployment)
- [Multi-currency portfolio](#multi-currency-portfolio)
- [Development](#development)
- [Project status & documentation map](#project-status--documentation-map)

## What it does

For every symbol on your watchlist, Atlas:

1. **Collects** real hourly OHLCV history from a live market data provider (Yahoo Finance's
   unofficial chart endpoint) and persists it to TimescaleDB.
2. **Computes technical indicators** — RSI, EMA, MACD, ATR, ADX, OBV, Bollinger Bands, volume
   analysis.
3. **Runs ML inference** — an XGBoost model (trained offline, exported to ONNX, run natively in
   Go) predicts correction probability; a second, independent model predicts recovery
   probability. A deterministic ADX+EMA rule classifies the current market regime
   (BULL/BEAR/SIDEWAYS) — no model needed for that one.
4. **Reads your actual portfolio** — current position, average cost, P&L, exposure, and
   portfolio-wide risk (drawdown/volatility/concentration) all feed into the decision, not just
   the symbol in isolation.
5. **Produces a recommendation** — BUY, ACCUMULATE, HOLD, REDUCE, SELL, or WAIT, each with a
   confidence score and an explanation listing which signals drove the call. Every recommendation
   is persisted (append-only) for full traceability.
6. **Notifies** — actionable calls (BUY/ACCUMULATE/REDUCE/SELL) push to Discord/Slack/Telegram
   (any combination); everything is scraped by Prometheus and visualized in Grafana.

Everything above is real and verified live end-to-end (real HTTP requests against a real Docker
stack, real TimescaleDB rows) — not a mockup. Where something is a known limitation (e.g. a
small/skewed ML training dataset), the docs say so explicitly instead of hiding it; see
[Machine learning](#machine-learning) below.

## Architecture

Clean Architecture, four layers, one Go binary:

```
API (HTTP handlers, request/response shaping)
  -> Application (use cases: collector, analyzer, ml, portfolio, decision, backtesting, alerts, auth, watchlist)
    -> Domain (market, portfolio, ml, decision, backtesting, currency, watchlist, alerts — pure types + business rules)
      -> Infrastructure (TimescaleDB repositories, Redis cache, Yahoo/NBP provider adapters, Discord webhook, ONNX runtime, Prometheus exporter)
```

- Business logic never lives in HTTP handlers.
- Every cross-cutting dependency (market provider, ML model, notification channel, exchange-rate
  source) sits behind an interface and is optional/non-fatal when unconfigured — Atlas always
  starts, degrading gracefully (e.g. falling back to a deterministic stub prediction when no ML
  model is loaded) rather than refusing to boot.
- A background loop (`internal/app/alerts_loop.go`, default every 5 minutes) evaluates the
  watchlist continuously, which is also what keeps real price history accumulating in the
  background for later ML training — no separate ingestion job needed.

See [docs/architecture.md](docs/architecture.md) and
[docs/decision_engine.md](docs/decision_engine.md) for the as-built detail, including the exact
scoring formula, and [docs/adr/](docs/adr/) for why the load-bearing decisions were made.

## Tech stack

| Concern | Choice |
|---|---|
| Backend | Go |
| Database | TimescaleDB (PostgreSQL + time-series hypertables) |
| Cache | Redis |
| ML training | Python — pandas, XGBoost, ONNX/onnxmltools (offline only, not deployed) |
| ML inference | Go, via `onnxruntime_go` (cgo, native `libonnxruntime`) |
| Web dashboard | Server-rendered `html/template`, Bootstrap 5.3 (vendored, no CDN/JS build) |
| Monitoring | Prometheus + Grafana |
| Alerting | Alertmanager (infra alerts) + Discord webhooks (business + infra, separate channels) |
| Deployment | Docker Compose (local/single-host) and Kubernetes manifests (`deploy/`) |

This is the project's only cgo dependency, which is why the container image is Debian
(`debian:bookworm-slim`), not Alpine — musl-based libc can't safely load the bundled
`libonnxruntime.so`. See the [Dockerfile](Dockerfile)'s comments for the full reasoning.

## Quickstart (Docker Compose)

Requirements: Docker + Docker Compose. Nothing else — no local Go/Python toolchain needed to run
the stack.

```bash
git clone <this repo>
cd atlas
docker compose up -d
```

That's it — every setting has a working default baked into `docker-compose.yml`, so the stack
comes up with zero configuration. Every container it defines:

| Service | Address | Persists in | Purpose |
|---|---|---|---|
| `atlas-api` | http://localhost:8080 | — | REST API + web dashboard |
| `timescaledb` | localhost:5432 | `timescaledb-data` | Primary database |
| `redis` | localhost:6379 | `redis-data` | Cache and session store |
| `prometheus` | http://localhost:9090 | `prometheus-data` | Metrics scraping |
| `alertmanager` | http://localhost:9093 | `alertmanager-data` | Infra alert routing |
| `grafana` | http://localhost:3000 | `grafana-data` | Dashboards (default login `admin`/`admin`) |
| `atlas-trainer` | internal only, port 8091 | `trainer-gomod-cache` | ML training sidecar. Same kind of long-lived container as the rest — HTTP service, healthcheck, `restart: unless-stopped` — but behind a Compose profile, so it needs `docker compose --profile trainer up -d` once; after that it stays up like everything else |

The trainer is gated that way because it builds a full Python/XGBoost toolchain nothing else
needs, and most sessions never train anything. It publishes no host port: atlas-api reaches it
in-network via `TRAINER_URL` and calls its `/train`, `/status` and `/delete` endpoints, which is
what backs the Account page's "Train new candidate" and model-delete buttons — training is
triggered on demand *through* a continuously running service, not by starting a one-shot
container. Until you start it those buttons just explain how to run `training/retrain.sh` by
hand, nothing else degrades, and `up{job="atlas-trainer"}` reads as down in Prometheus, which is
expected rather than a fault.

**What persists.** The named volumes above survive `docker compose down` and are destroyed by
`docker compose down -v`. Trained models are the exception: `./training/models` is a bind mount,
read-only into atlas-api and read-write into the trainer, which is how a model the sidecar trains
becomes selectable on atlas-api's Account page without a rebuild.

Open http://localhost:8080/ for the web UI, or start collecting history for a symbol
directly via the API:

```bash
curl "http://localhost:8080/prices?symbol=AAPL&window=60"
curl "http://localhost:8080/recommendations?symbol=AAPL"
```

To use real secrets (Discord webhooks, a non-default DB password, admin login) instead of the
baked-in defaults, copy `.env.example` to `.env` and fill it in — `.env` is gitignored, so real
values never get committed. See [Configuration](#configuration) below for every variable.

## Configuration

Every setting is an environment variable (`internal/config/config.go`), and every optional
integration is non-fatal when unset — Atlas degrades gracefully instead of refusing to start.

| Variable | Default | Purpose |
|---|---|---|
| `SERVER_PORT` | `8080` | HTTP listen port |
| `LOG_LEVEL` | `info` | zap log level |
| `DATABASE_HOST`/`PORT`/`NAME`/`USER`/`PASSWORD` | `timescaledb`/`5432`/`atlas`/`atlas`/`atlas` | TimescaleDB connection |
| `DATABASE_SSLMODE` | `disable` | libpq SSL mode for the database connection. Rejected at startup when `COOKIE_SECURE=true` — a TLS-fronted deployment must not reach its own database in plaintext. Use `require`, or `verify-full` once the server presents a trusted certificate |
| `REDIS_HOST`/`PORT` | `redis`/`6379` | Redis connection |
| `REDIS_PASSWORD` | *(empty → no auth)* | Redis `AUTH` password; the Compose `redis` service picks up the same variable as its `requirepass`, so the two can't drift. Required when `COOKIE_SECURE=true` |
| `REDIS_TLS` | `false` | Wrap the Redis connection in TLS 1.2+. Required when `COOKIE_SECURE=true` |
| `COLLECTOR_WINDOW_DAYS` | `30` | How much history to request per collection call |
| `ALERT_POLL_INTERVAL_SECONDS` | `300` | Background watchlist-evaluation interval |
| `DERIVED_RETENTION_DAYS` | `0` *(keep everything)* | Days of recomputable history (indicators, recommendations, predictions) to keep; pruned once a day. The alert loop writes one recommendation per watched symbol per poll — including `HOLD` — so these tables grow without bound on a long-lived instance, and `BackupAll` builds the whole database as one in-memory JSON document |
| `DISCORD_WEBHOOK_URL` | *(empty → disabled)* | Business alerts: BUY/ACCUMULATE/REDUCE/SELL notifications, sent only when a symbol's action *changes* |
| `SLACK_WEBHOOK_URL` | *(empty → disabled)* | Same business alerts as `DISCORD_WEBHOOK_URL`, delivered to a Slack Incoming Webhook instead/as well |
| `TELEGRAM_BOT_TOKEN` / `TELEGRAM_CHAT_ID` | *(empty → disabled)* | Same business alerts, delivered via a Telegram bot; both must be set |
| `ALERTMANAGER_DISCORD_WEBHOOK_URL` | *(empty → disabled)* | Infra alerts: pod down/flapping/high error rate — deliberately a **separate** webhook/channel from the ones above |
| `ML_ENABLED` | `true` | Seeds the decision engine's ML blend. Set to `false` and scoring uses the technical term alone (RSI/MACD/Bollinger/momentum), skipping the ML+Portfolio+Risk blend entirely rather than falling back to the RSI-derived stub. Only the *initial* value — the Account page toggles this live and its choice, once made, wins over this variable |
| `MODEL_PATH` | *(empty → deterministic stub)* | Path to a trained correction-probability ONNX model |
| `RECOVERY_MODEL_PATH` | *(empty → recovery probability stays 0)* | Path to a trained recovery-probability ONNX model, fully independent of `MODEL_PATH` |
| `ML_MODELS_DIR` | `/models` | Directory the Account page's model picker scans for trained models + metadata sidecars |
| `LIBONNXRUNTIME_PATH` | Dockerfile default (`/usr/lib/libonnxruntime.so`) | Native ONNX Runtime library location |
| `TRAINER_URL` | *(empty → training/delete buttons disabled)* | Internal URL of the optional `atlas-trainer` sidecar (`docker compose --profile trainer up -d`), which powers the Account page's "Train new candidate" and model-delete actions |
| `PROMETHEUS_URL`/`ALERTMANAGER_URL`/`GRAFANA_URL` | `http://prometheus:9090`/`http://alertmanager:9093`/`http://grafana:3000` | Checked by `/health` and the `atlas_dependency_up` gauge. Non-empty by default because all three are always in the default Compose stack; set one to an empty string to disable that check |
| `ADMIN_USERNAME`/`ADMIN_PASSWORD_HASH` | *(empty → no login gate)* | Enables password+TOTP auth for the dashboard/API — generate a hash with `go run ./cmd/generate-admin-credentials`. Login is locked out globally (not per-IP — there's only ever one account) for 15 minutes after 5 failed attempts in a row. |
| `COOKIE_SECURE` | `false` | Adds the `Secure` flag to the session cookie — set to `true` once Atlas is actually served over HTTPS (e.g. `deploy/ingress.yaml`'s TLS termination); leave `false` for the plain-HTTP local Compose stack. |

Database migrations are applied by the API itself on every start, under a Postgres advisory lock
and tracked in `schema_migrations` (`internal/infrastructure/persistence/migrate.go`). The
`./migrations` mount in `docker-compose.yml` only pre-seeds a brand-new volume; an existing
database no longer needs migrations applied by hand.

Architecture decisions that are not obvious from the code live in [docs/adr/](docs/adr/) — one
short file per decision, with its trade-off and what would make it wrong.

`GRAFANA_ADMIN_PASSWORD` and `DATABASE_PASSWORD` are also read directly by `docker-compose.yml`
for the `grafana`/`timescaledb` containers themselves. See `.env.example` for the full,
copy-pasteable list with inline comments.

**`.env` gotcha:** a bcrypt hash (`ADMIN_PASSWORD_HASH`) contains literal `$` characters, which
Docker Compose's variable interpolation will otherwise silently eat — confirmed live: pasting a
real hash straight into `.env` truncated it mid-string and made every login fail with no error
explaining why. Escape every `$` as `$$` in `.env` (not in the shell, not in `docker-compose.yml`
itself — only in the `.env` file), e.g. `$2a$10$Slp...` becomes `$$2a$$10$$Slp...`.

## Web dashboard

A server-rendered dashboard at `/` (Bootstrap 5.3, light/dark/auto theme, no separate frontend
build — one Go binary serves everything via `go:embed`). Installable as a home-screen app on
phones/desktop (a Web App Manifest + `apple-touch-icon`, no service worker — it always loads live,
same as a normal page):

- **Portfolio** — record BUY/SELL transactions, view positions/P&L/risk, delete individual
  positions/transactions or bulk-delete either via checkboxes (position cost basis is
  recomputed from the remaining transaction history, not patched in place).
- **Recommendations** — run the decision engine against any watchlist symbol.
- **Alerts** — view current non-HOLD signals across the watchlist, each with how long it has held
  that action. That span comes from walking the symbol's recommendation history back to where the
  action last changed, not from the alert row's own timestamp — every row is stamped with the
  moment it was computed, so the newest one always reads as "just now" however long the signal has
  actually stood.
- **Backtests** — run a historical simulation and view past results.
- **Watchlist** — add tracked symbols, remove them individually or in bulk via checkboxes, and
  export/import the whole list as a JSON file to move it between environments without
  re-entering several dozen symbols by hand.
- **Account** — change password, enroll/disable TOTP two-factor auth via a real in-app QR code,
  and pick which trained ML model (correction/recovery, independently) is active — see
  [Machine learning](#machine-learning) below.

Every table filters, paginates, and sorts client-side (click a column header; the caret shows the
current order), and every symbol links to its Yahoo Finance quote page — the same source Atlas
collects its bars from, so a ticker Atlas tracks is by construction one that page resolves.

Discord alerts arrive as embeds rather than a single line: colour-coded by action, titled with the
symbol and linked to its quote page, with confidence/score/market condition as a field row and the
rationale as a list.

Authentication is opt-in: leave `ADMIN_USERNAME`/`ADMIN_PASSWORD_HASH` unset and there's no login
gate at all (matches every curl-based workflow above). Set both and every route except
`/health`, `/metrics`, `/login`, and `/static/*` requires a session — the dashboard redirects to
`/login`, while the raw JSON API returns a real `401` instead. English, Polish, German, French, and
Spanish are all fully supported, switchable per-request, persisted in a cookie.

## REST API

All endpoints are JSON except the dashboard/auth routes, which serve HTML. Full request/response
shapes: [docs/api.md](docs/api.md).

| Endpoint | Method | Purpose |
|---|---|---|
| `/health` | GET | Liveness check, plus a per-dependency status for database/redis/trainer/prometheus/alertmanager/grafana. Only database and redis affect the overall status (and the 503) — atlas-api can serve requests without the rest |
| `/metrics` | GET | Prometheus exposition |
| `/swagger/index.html` | GET | Interactive API docs, generated from the handler annotations — "Try it out" reuses your dashboard session |
| `/symbols` | GET | List known symbols |
| `/prices` | GET | Fetch/collect OHLCV history for a symbol |
| `/portfolio` | GET/POST/DELETE | View positions + risk / record a transaction / clear a symbol's position aggregate (`?symbol=`) |
| `/transactions` | GET/DELETE | List recorded transactions / remove one by id (`?id=`) and recompute the affected position |
| `/watchlist` | GET/POST/DELETE | View / add / remove (`?symbol=`) tracked symbols |
| `/watchlist/export` | GET | Download the watchlist as a JSON file |
| `/watchlist/import` | POST | Apply an exported watchlist — `?mode=replace` mirrors the file exactly, default merges |
| `/alerts` | GET | Evaluate the watchlist for non-HOLD signals |
| `/recommendations` | GET | Run the decision engine for a symbol |
| `/predictions` | GET | Raw ML model output for a symbol |
| `/backtests` | GET | Run a historical simulation |
| `/backtests/history` | GET | Past backtest runs |
| `/backtests/walkforward` | GET | Walk-forward threshold optimization (report-only, never auto-applied) |

## Machine learning

Two independent, optional binary classifiers (XGBoost → ONNX), trained offline on real collected
history. No shipped model is ever trained on synthetic data; the one synthetic generator in the
repo (`training/make_synthetic_features.py`) exists solely so CI can run a full training pass
end-to-end without a database, and its output never becomes a candidate.

- **Correction probability** (`MODEL_PATH`) — probability of a drop within the next 10 bars past a
  barrier of **4x the instrument's own trailing 20-bar realised volatility, floored at 2%**. It is
  deliberately not a fixed 5%: that asked a different question of every instrument (GLD never
  qualified, SOXL qualified in 58% of windows), which is a large part of why a per-symbol
  frequency lookup was so hard to beat.
- **Recovery probability** (`RECOVERY_MODEL_PATH`) — conditional on a correction having actually
  happened, probability it recovers to its pre-drop price within N bars. Structurally thinner
  training data than the correction model, since recovery is only a defined question *after* a
  correction.

Every feature is scale-free by construction (`rsi, ema_dist, macd_pct, tr_pct, atr_pct, rvol_20,
rvol_50, volume` — ratios and bounded indices, never a raw price). That is enforced rather than
assumed: feeding raw price-unit indicators let an earlier version of the model identify the
symbol instead of reading its dynamics, and it scored *worse* than a table of per-symbol
correction frequencies while reporting a reassuring 92% accuracy. Every training run now measures
itself against that same baseline and says so plainly when it loses.

Both models currently beat that baseline on **PR AUC** — correction 0.264 vs 0.188, recovery
0.851 vs 0.651. PR AUC rather than ROC AUC is the deliberate yardstick here: Atlas alerts on the
top of the ranking, not the uneventful middle. Restricted to its 25 highest-scoring bars the
correction model was right **68% of the time against a 6.5% base rate** — a 10.5x lift, where the
same alert budget spent on the baseline returned 16%. It also scores instruments it has never
trained on (0.732 ROC AUC) where the baseline scores exactly 0.500, having no history for a
newly-watchlisted ticker. See [docs/ml.md](docs/ml.md) for every number and the measurement
behind it.

Neither model is loaded by default — Atlas falls back to a deterministic RSI-derived stub
prediction (correction) / leaves recovery probability at `0` (recovery), both logged clearly at
startup, not treated as an error. A **market condition** classifier also exists
(BULL/BEAR/SIDEWAYS) but is deliberately *not* ML — it's a deterministic ADX+EMA technical rule,
since that doesn't need labeled historical outcomes to define.

Training reuses the exact same Go feature-computation code the live inference path uses
(`cmd/export-training-data` calls the real analyzer), so training and serving can never silently
drift apart. Full runbook, including how to point a running stack at a freshly trained model:
[training/README.md](training/README.md). Model files are gitignored local/CI artifacts, never
committed source.

**Generating candidates is automatable; promoting one is always a human decision.**
[training/retrain.sh](training/retrain.sh) exports fresh data and trains a new, uniquely-versioned
candidate for both models (never overwriting an existing file) — schedule it with cron/a systemd
timer to regenerate candidates as more real history accumulates. It never changes what's live: a
new candidate only starts influencing real recommendations once picked from the **Account**
dashboard page (`/dashboard/account`), which lists every trained model with its real holdout AUC
against the per-symbol base-rate baseline, its accuracy and row count — and an explicit "DOES NOT
BEAT BASELINE" marker when that applies — then swaps it in immediately (no restart needed) while
durably recording the choice (`migrations/011`) so it survives one. This is deliberate, not just
unfinished: a human should look at each new model's metrics before it starts driving real
recommendations — the same "never auto-promote" stance already applied to walk-forward threshold
optimization above. The baseline marker exists because that judgement call is only as good as the
numbers it's shown, and accuracy alone made a model that had learned nothing look excellent. Both training and deleting old candidates can also be
triggered straight from the Account page's buttons, via an internal-only sidecar container
(`cmd/trainer`, off by default — see training/README.md's "Triggering training from the
dashboard") — atlas-api's own model directory stays read-only either way.

## Backtesting

A historical simulation engine (`internal/application/backtesting`) that judges each action from
its own simulated position (never leaking live portfolio state), reports Returns/Sharpe/
Volatility/Max Drawdown/Win Rate, and scores the loaded ML model's predictions against realized
outcomes (Accuracy/Precision/Recall/F1/ROC AUC). Every run persists to `backtest_runs`
(queryable via `/backtests/history`). Walk-forward train/validate/test splitting and a small
threshold grid search are also real (`/backtests/walkforward`) — results are report-only and
never auto-applied to the live decision engine. Full detail: [docs/backtesting.md](docs/backtesting.md).

## Observability

- `/metrics` exposes HTTP, provider (labeled by outcome and kind — historical bars vs. a live
  quote), decision, ML, and alert-delivery counters (Prometheus format), scraped by
  `prometheus/prometheus.yml` and visualized via `grafana/dashboards/`. Alongside them
  sits one gauge, `atlas_dependency_up{dependency="…"}`, refreshed every 15s from the same checks
  `/health` runs — so a dependency outage is graphable over time, not just visible in a
  point-in-time JSON response. atlas-api's own liveness is Prometheus' built-in
  `up{job="atlas-api"}` instead (a process that's down can't push a gauge saying so); both render
  together on the Grafana dashboard's "Service Status" panel.
- The optional `atlas-trainer` sidecar exposes its **own** `/metrics` on port 8091, scraped as a
  second Prometheus job — `atlas_trainer_runs_total{outcome}`,
  `atlas_trainer_run_duration_seconds`, `atlas_trainer_last_run_timestamp_seconds`. The last of
  those drives a "Time Since Last Training Run" panel, so retraining going stale is visible
  without anyone remembering to check. Its scrape target reads as down whenever the `trainer`
  profile isn't started, which is expected rather than broken.
- Infrastructure alerting rules (`prometheus/alerts.yml`: API down, crash-loop/flapping, high
  error rate, background alert-poll failures) route through Alertmanager to a Discord channel —
  deliberately separate from the business-alert webhook above, so infra noise never mixes with
  investment notifications. A *failed* dashboard-triggered training run posts to the same channel
  via `TRAINING_DISCORD_WEBHOOK_URL` (sourced from `ALERTMANAGER_DISCORD_WEBHOOK_URL` in `.env`) —
  `cmd/trainer` sends that, the one outcome retrain.sh's `set -e` never lets it reach itself. A
  successful run only logs; it doesn't push anywhere (a "new candidates ready" ping used to fire
  on every run regardless of whether the candidate was any good, which was noise, not signal).
- Structured JSON logs throughout (zap).

## Kubernetes deployment

`deploy/` contains manifests for `atlas-api` (namespace, configmap, secret template, deployment
with a non-root/read-only-root security context, `/health` probes, service, ingress, HPA, PDB)
plus a daily TimescaleDB backup `CronJob` (7-day retention). Secret management is deliberately
manual `kubectl create secret` — no Sealed Secrets/External Secrets Operator, judged more than
this project's single-user scale needs. A disaster-recovery runbook
([deploy/RUNBOOK.md](deploy/RUNBOOK.md)) has been rehearsed for real, twice — once against Docker
Compose, once against a real Kubernetes cluster (minikube) — and documents two real bugs each
drill found and fixed. Testing against a real managed/production cluster and off-cluster backup
storage (S3/GCS/etc.) are explicitly out of scope for this project. See
[deploy/README.md](deploy/README.md) for the full apply instructions.

## Multi-currency portfolio

Transactions can be recorded in PLN, EUR, or USD. Each is converted to PLN once, at the moment
it's recorded, using that day's real NBP (Polish central bank) reference rate — never
recomputed later, so a position's cost basis only changes when a real transaction is added or
removed, not from FX movement alone. All internal math (Decision Engine, risk, ROI, backtesting)
works in PLN only and never needs to know multi-currency exists; the dashboard shows a live
secondary EUR/USD conversion wherever a PLN value is displayed.

## Development

```bash
go build ./...
go vet ./...
gofmt -l .          # should print nothing
go test ./...
```

Three groups of tests are gated on an environment variable and skip silently without it, so a
plain `go test ./...` on a laptop does not cover them: `TEST_DATABASE_DSN` (TimescaleDB),
`TEST_REDIS_ADDR` (Redis) and `TEST_ONNXRUNTIME_LIB_PATH` (real ONNX inference). CI sets all
three — the first two via service containers, the third by installing the same ONNX Runtime
version the production image ships — so nothing is left permanently unexercised. To run the ONNX
ones locally, point the variable at a real `libonnxruntime.{so,dylib}`, e.g. the one bundled with
`pip install onnxruntime`.

The fixtures those ONNX tests load are generated by
`internal/application/ml/testdata/gen_smoketest.py`, which must be re-run whenever
`ml.FeatureOrder` changes: the `.onnx` graph encodes the input width, so adding a feature on the
Go side without regenerating leaves a model that loads and then fails at inference.

CI (`.github/workflows/ci.yml`) runs the same checks plus `govulncheck` and a Docker build
validation against real TimescaleDB + Redis service containers on every PR. The `Push`/`Deploy`
stages (publishing images, deploying to a cluster) are explicitly out of scope — this project
doesn't have a real container registry or production cluster to target.

## Project status & documentation map

| Document | What it's for |
|---|---|
| [implementation-plan.md](implementation-plan.md) | One paragraph of status per phase, plus what is deliberately not built and why |
| [PROJECT_CONTEXT.md](PROJECT_CONTEXT.md) | Component-by-component architecture reference with the same honest implementation-status treatment |
| [docs/](docs/) | Deep dives: `architecture.md`, `decision_engine.md`, `ml.md`, `backtesting.md`, `database.md`, `deployment.md`, `api.md` |
| [training/README.md](training/README.md) | ML training pipeline runbook |
| [deploy/README.md](deploy/README.md), [deploy/RUNBOOK.md](deploy/RUNBOOK.md) | Kubernetes apply instructions and the disaster-recovery runbook |
| [GETTING_STARTED.md](GETTING_STARTED.md) | Plain-language walkthrough for using the dashboard — no technical background assumed |
| [PRODUCT.md](PRODUCT.md) | What Atlas is for and who it's for, ahead of how it's built |
| [docs/adr/](docs/adr/) | The decisions that are expensive to reverse, and what would have to be true to revisit them |
| [CLAUDE.md](CLAUDE.md) | The standing rules this codebase is held to, by domain (Go, finance, ML, TimescaleDB, Docker, observability) — mostly rules that exist because the alternative was tried and cost something |

As of this writing, every phase in `implementation-plan.md` is either fully implemented or
explicitly marked out of scope — the only intentionally excluded items are multi-user accounts,
OAuth2/OIDC, and a full SPA frontend, none of which fit this project's "single-user personal
tool" scope today.
