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
authoritative detail and this README is a map, not a duplicate. [Supported environment and known
limitations](#supported-environment-and-known-limitations) is the one current list of what Atlas
does not do.

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
- [Multi-currency portfolio](#multi-currency-portfolio)
- [Development](#development)
- [Supported environment and known limitations](#supported-environment-and-known-limitations)
- [Documentation map](#documentation-map)

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
  -> Application (use cases: collector, analyzer, ml, portfolio, decision, backtesting, alerts, watchlist, exchangerate, notifications)
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
| Deployment | Docker Compose, on one machine, for one person |

This is the project's only cgo dependency, which is why the container image is Debian
(`debian:bookworm-slim`), not Alpine — musl-based libc can't safely load the bundled
`libonnxruntime.so`. The image builds for `linux/amd64` and `linux/arm64` (Apple Silicon, ARM
servers), each with its own native ONNX Runtime; see the [Dockerfile](Dockerfile)'s comments.
amd64 is verified end to end, trained models included. arm64 builds and serves correctly, but
loading ONNX Runtime has only been attempted under emulation, where it crashes for Microsoft's own
arm64 build as well — model inference on arm64 still needs one run on real ARM hardware.

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
| `redis` | localhost:6379 | `redis-data` | Cache for price bars and exchange rates |
| `prometheus` | http://localhost:9090 | `prometheus-data` | Metrics scraping |
| `alertmanager` | http://localhost:9093 | `alertmanager-data` | Infra alert routing |
| `grafana` | http://localhost:3000 | `grafana-data` | Dashboards (default login `admin`/`admin`) |
| `atlas-trainer` | internal only, port 8091 | `trainer-gomod-cache` | ML training sidecar. Same kind of long-lived container as the rest — HTTP service, healthcheck, `restart: unless-stopped` — and started by a plain `docker compose up` like everything else |

The trainer publishes no host port: atlas-api reaches it in-network via `TRAINER_URL` and calls
its `/train`, `/status` and `/delete` endpoints, which is what backs the Settings page's "Train new
candidate" and model-delete buttons — training is triggered on demand *through* a continuously
running service, not by starting a one-shot container. Set `TRAINER_URL=` (empty) to hide those
buttons; they then explain how to run `training/retrain.sh` by hand, and nothing else degrades.

**Every port is bound to `127.0.0.1`, and there is no login.** Atlas is a local tool for one
person: anyone who can reach the dashboard can read and change everything in it, so it must not
be published beyond the machine it runs on.

**What persists.** The named volumes above survive `docker compose down` and are destroyed by
`docker compose down -v`. Trained models are the exception: `./training/models` is a bind mount,
read-only into atlas-api and read-write into the trainer, which is how a model the sidecar trains
becomes selectable on atlas-api's Settings page without a rebuild.

Open http://localhost:8080/ for the web UI, or start collecting history for a symbol
directly via the API:

```bash
curl "http://localhost:8080/prices?symbol=AAPL&window=60"
curl "http://localhost:8080/recommendations?symbol=AAPL"
```

To use real secrets (Discord webhooks, a non-default DB password) instead of the
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
| `DATABASE_SSLMODE` | `disable` | libpq SSL mode for the database connection — `disable` matches the Compose stack's Postgres; use `require` or `verify-full` for a Postgres that serves TLS |
| `REDIS_HOST`/`PORT` | `redis`/`6379` | Redis connection |
| `REDIS_PASSWORD` | *(empty → no auth)* | Redis `AUTH` password; the Compose `redis` service picks up the same variable as its `requirepass`, so the two can't drift |
| `REDIS_TLS` | `false` | Wrap the Redis connection in TLS 1.2+, for a Redis that serves it |
| `COLLECTOR_WINDOW_DAYS` | `30` | How much history to request per collection call |
| `ALERT_POLL_INTERVAL_SECONDS` | `300` | Background watchlist-evaluation interval |
| `DERIVED_RETENTION_DAYS` | `0` *(keep everything)* | Days of recomputable history (indicators, recommendations, predictions) to keep; pruned once a day. The alert loop writes one recommendation per watched symbol per poll — including `HOLD` — so these tables grow without bound on a long-lived instance, and every backup and restore carries all of it |
| `DISCORD_WEBHOOK_URL` | *(empty → disabled)* | Business alerts: BUY/ACCUMULATE/REDUCE/SELL notifications, sent only when a symbol's action *changes* |
| `SLACK_WEBHOOK_URL` | *(empty → disabled)* | Same business alerts as `DISCORD_WEBHOOK_URL`, delivered to a Slack Incoming Webhook instead/as well |
| `TELEGRAM_BOT_TOKEN` / `TELEGRAM_CHAT_ID` | *(empty → disabled)* | Same business alerts, delivered via a Telegram bot; both must be set |
| `ALERTMANAGER_DISCORD_WEBHOOK_URL` | *(empty → disabled)* | Infra alerts: API down/flapping, high error rate, failing alert polls, failed training runs — deliberately a **separate** webhook/channel from the ones above |
| `ML_ENABLED` | `true` | Seeds whether the ML factor takes part in scoring. `false` drops only the ML term: the portfolio and portfolio-risk factors still apply, since they are independent of the model. Only the *initial* value — the Settings page toggles this live and its choice, once made, wins over this variable |
| `MODEL_PATH` | *(empty → deterministic stub)* | Path to a trained correction-probability ONNX model |
| `RECOVERY_MODEL_PATH` | *(empty → recovery probability stays 0)* | Path to a trained recovery-probability ONNX model, fully independent of `MODEL_PATH` |
| `ML_MODELS_DIR` | `/models` | Directory the Settings page's model picker scans for trained models + metadata sidecars |
| `LIBONNXRUNTIME_PATH` | Dockerfile default (`/usr/lib/libonnxruntime.so`) | Native ONNX Runtime library location |
| `TRAINER_URL` | Compose: `http://atlas-trainer:8091`; otherwise empty → training/delete buttons disabled | Internal URL of the `atlas-trainer` sidecar, which powers the Settings page's "Train new candidate" and model-delete actions. Compose runs the trainer by default and points this at it; set it empty to hide the buttons |
| `PROMETHEUS_URL`/`ALERTMANAGER_URL`/`GRAFANA_URL` | `http://prometheus:9090`/`http://alertmanager:9093`/`http://grafana:3000` | Checked by `/health` and the `atlas_dependency_up` gauge. Non-empty by default because all three are always in the default Compose stack; set one to an empty string to disable that check |


Database migrations are applied by the API itself on every start, under a Postgres advisory lock
and tracked in `schema_migrations` (`internal/infrastructure/persistence/migrate.go`). The
`./migrations` mount in `docker-compose.yml` only pre-seeds a brand-new volume; an existing
database no longer needs migrations applied by hand.

Architecture decisions that are not obvious from the code live in [docs/adr/](docs/adr/) — one
short file per decision, with its trade-off and what would make it wrong.

`GRAFANA_ADMIN_PASSWORD` and `DATABASE_PASSWORD` are also read directly by `docker-compose.yml`
for the `grafana`/`timescaledb` containers themselves. See `.env.example` for the full,
copy-pasteable list with inline comments.

## Web dashboard

A server-rendered dashboard at `/` (Bootstrap 5.3, light/dark/auto theme, no separate frontend
build — one Go binary serves everything via `go:embed`). Installable as a home-screen app on
phones/desktop (a Web App Manifest + `apple-touch-icon`, no service worker — it always loads live,
same as a normal page):

- **Portfolio** — record BUY/SELL transactions with their trade date (a EUR/USD trade is converted
  at that day's NBP rate), view positions/P&L/risk, delete transactions singly or in bulk (the
  position is recomputed from the remaining history, not patched in place), or hide a position —
  which keeps its transactions, so the next trade on that symbol rebuilds it. A price shown under
  a position says whether it is a current quote, a stale one, or only the last trade price. A form
  submitted twice records one transaction.
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
- **Settings** — manage portfolios, pick which trained ML model (correction/recovery,
  independently) is active and toggle the ML factor — see [Machine learning](#machine-learning)
  below — and back up, restore or wipe all data. A backup restores across schema upgrades; wiping
  is refused unless a backup was taken in the last five minutes.

Every table filters, paginates, and sorts client-side (click a column header; the caret shows the
current order), and every symbol links to its Yahoo Finance quote page — the same source Atlas
collects its bars from, so a ticker Atlas tracks is by construction one that page resolves.

Discord alerts arrive as embeds rather than a single line: colour-coded by action, titled with the
symbol and linked to its quote page, with confidence/score/market condition as a field row and the
rationale as a list.

A recommendation made with an input missing (ML prediction, portfolio state, or a current price
for a held position) is marked "incomplete inputs" on the Alerts and Recommendations pages and in
alert embeds, and that flag is stored with it. There is no login. English, Polish, German, French,
and Spanish are all fully supported — messages included — switchable per-request, persisted in a
cookie.

## REST API

All endpoints are JSON except the dashboard routes, which serve HTML. Full request/response
shapes: [docs/api.md](docs/api.md).

| Endpoint | Method | Purpose |
|---|---|---|
| `/health` | GET | Liveness check, plus a per-dependency status for database/redis/trainer/prometheus/alertmanager/grafana. Only database and redis affect the overall status (and the 503) — atlas-api can serve requests without the rest |
| `/metrics` | GET | Prometheus exposition |
| `/swagger/index.html` | GET | Interactive API docs, generated from the handler annotations |
| `/symbols` | GET | List known symbols |
| `/prices` | GET | Fetch/collect OHLCV history for a symbol |
| `/portfolio` | GET/POST/DELETE | View positions + risk / record a transaction (an `id` makes a retry safe) / hide a symbol's position, keeping its transactions (`?symbol=`) |
| `/transactions` | GET/DELETE | List recorded transactions / remove one by id (`?id=`) and recompute the affected position |
| `/watchlist` | GET/POST/DELETE | View / add / remove (`?symbol=`) tracked symbols |
| `/watchlist/export` | GET | Download the watchlist as a JSON file |
| `/watchlist/import` | POST | Apply an exported watchlist — `?mode=replace` mirrors the file exactly, default merges |
| `/alerts` | GET | The watchlist's current non-HOLD signals, as last computed by the background poll |
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

PR AUC rather than ROC AUC is the yardstick: Atlas alerts on the top of the ranking, not the
uneventful middle. The development history in [docs/ml.md](docs/ml.md) records the measurements
that shaped the model (for example correction PR AUC 0.264 against a 0.188 baseline on the run
that first cleared it).

The two model files currently in `training/models/` state their own numbers in their metadata:
correction `v20260810145759` has PR AUC 0.347 against a 0.346 baseline, recovery `v20260809134009`
0.758 against 0.528. Both predate the exporter fix that applies the volatility-scaled barrier, and
neither records the label it was trained on (`label_params`), so their labels are the older fixed
barrier and a backtest scores them against the default label with a warning. Retrain both before
reading their metrics as measurements of the target described above.

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
new candidate only starts influencing real recommendations once picked from the **Settings**
dashboard page (`/dashboard/settings`), which lists every trained model with its real holdout PR
AUC against the per-symbol base-rate baseline, its ROC AUC and row counts — and an explicit "DOES NOT
BEAT BASELINE" marker when that applies — then swaps it in immediately (no restart needed) while
durably recording the choice (`migrations/011`) so it survives one. This is deliberate, not just
unfinished: a human should look at each new model's metrics before it starts driving real
recommendations — the same "never auto-promote" stance already applied to walk-forward threshold
optimization above. The baseline marker exists because that judgement call is only as good as the
numbers it's shown, and accuracy alone made a model that had learned nothing look excellent. Both training and deleting old candidates can also be
triggered straight from the Settings page's buttons, via an internal-only sidecar container
(`cmd/trainer`, part of the default Compose stack — see training/README.md's "Triggering training
from the dashboard") — atlas-api's own model directory stays read-only either way.

## Backtesting

A historical simulation engine (`internal/application/backtesting`) that judges each action from
its own simulated position (never leaking live portfolio state), reports Returns/Sharpe/
Volatility/Max Drawdown/Win Rate, and scores the loaded ML model's predictions against realized
outcomes (Accuracy/Precision/Recall/F1/ROC AUC), using the label the model was trained on and
saying so when the model does not record it. Every run persists to `backtest_runs`
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
- The `atlas-trainer` sidecar exposes its **own** `/metrics` on port 8091, scraped as a
  second Prometheus job — `atlas_trainer_runs_total{outcome}`,
  `atlas_trainer_run_duration_seconds`, `atlas_trainer_last_run_timestamp_seconds`. The last of
  those drives a "Time Since Last Training Run" panel, so retraining going stale is visible
  without anyone remembering to check. Its scrape target reads as down only while the
  trainer container is stopped.
- Infrastructure alerting rules (`prometheus/alerts.yml`: API down, crash-loop/flapping, high
  error rate, background alert-poll failures) route through Alertmanager to a Discord channel —
  deliberately separate from the business-alert webhook above, so infra noise never mixes with
  investment notifications. A *failed* dashboard-triggered training run posts to the same channel
  via `TRAINING_DISCORD_WEBHOOK_URL` (sourced from `ALERTMANAGER_DISCORD_WEBHOOK_URL` in `.env`) —
  `cmd/trainer` sends that, the one outcome retrain.sh's `set -e` never lets it reach itself. A
  successful run only logs; it doesn't push anywhere (a "new candidates ready" ping used to fire
  on every run regardless of whether the candidate was any good, which was noise, not signal).
- Structured JSON logs throughout (zap).

## Multi-currency portfolio

Transactions can be recorded in PLN, EUR, or USD. Each is converted to PLN once, when it's
recorded, using the real NBP (Polish central bank) reference rate for the trade's own date — never
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

The Go toolchain is 1.27.1 (`go.mod`, both Dockerfiles). The Python tests (`training/test_split.py`)
need the pinned `training/requirements.txt`, which targets Python 3.11 — run them in a throwaway
container rather than a newer host Python:

```bash
docker run --rm -v "$PWD:/app" -w /app python:3.11-slim-bookworm sh -c \
  'python -m venv /tmp/v && /tmp/v/bin/pip install -q -r training/requirements.txt pytest && /tmp/v/bin/python -m pytest -q -p no:cacheprovider training/'
```

Three groups of tests are gated on an environment variable and skip silently without it, so a
plain `go test ./...` on a laptop does not cover them: `TEST_DATABASE_DSN` (TimescaleDB),
`TEST_REDIS_ADDR` (Redis) and `TEST_ONNXRUNTIME_LIB_PATH` (real ONNX inference). Point the first
two at disposable containers, never at the database you use: the persistence tests restore and
flush whole databases. CI sets all
three — the first two via service containers, the third by installing the same ONNX Runtime
version the production image ships — so nothing is left permanently unexercised. To run the ONNX
ones locally, point the variable at a real `libonnxruntime.{so,dylib}`, e.g. the one bundled with
`pip install onnxruntime`.

The fixtures those ONNX tests load are generated by
`internal/application/ml/testdata/gen_smoketest.py`, which must be re-run whenever
`ml.FeatureOrder` changes: the `.onnx` graph encodes the input width, so adding a feature on the
Go side without regenerating leaves a model that loads and then fails at inference.

CI (`.github/workflows/ci.yml`) runs the same checks plus `govulncheck` and a Docker build
validation against real TimescaleDB + Redis service containers on every PR. There is no push or
deploy stage: Atlas runs where it is built.

## Supported environment and known limitations

**Supported:** one person, on one machine, with Docker Compose; `linux/amd64` (verified) or
`linux/arm64` (builds and runs; model inference not yet verified on real ARM hardware); the service versions pinned in `docker-compose.yml`. Market data comes from Yahoo
Finance's unofficial API and exchange rates from NBP.

**Deliberately not part of Atlas**, and not unfinished work: executing trades or any brokerage
connection, logins or multiple users, exposure beyond `127.0.0.1`, Kubernetes or any multi-host
deployment, running more than one `atlas-api` at a time against one database, and a separate SPA
frontend.

**Known limitations:**

- The technical score was measured as having no selection value on 720 days of hourly bars
  ([docs/decision_engine.md](docs/decision_engine.md)); Atlas has no measured entry signal.
- The ML models in `training/models/` predate the current label definition and should be
  retrained (see [Machine learning](#machine-learning)).
- Backups do not leave the machine and do not run on a schedule: copy them elsewhere
  ([docs/runbook.md](docs/runbook.md)).
- Messages carried in from a lower-layer error (a provider or database failure) stay in English;
  everything else on the dashboard follows the chosen language.
- Browser checks so far are server-side renders of every page in every language plus HTTP-level
  tests; there is no automated browser, phone or keyboard test run.

## Documentation map

| Document | What it's for |
|---|---|
| [docs/](docs/) | Deep dives: `architecture.md`, `decision_engine.md`, `ml.md`, `backtesting.md`, `database.md`, `deployment.md`, `api.md` |
| [docs/runbook.md](docs/runbook.md) | Backup, restore and recovery procedures, rehearsed |
| [training/README.md](training/README.md) | ML training pipeline runbook |
| [GETTING_STARTED.md](GETTING_STARTED.md) | Plain-language walkthrough for using the dashboard — no technical background assumed |
| [docs/adr/](docs/adr/) | The decisions that are expensive to reverse, and what would have to be true to revisit them |
