# Atlas — AI Portfolio Advisor

Atlas is a **decision-support** tool for a personal stock/ETF portfolio. It watches a list of
symbols, computes technical indicators, runs ML models, and combines them with your actual
portfolio into an explained BUY/ACCUMULATE/HOLD/REDUCE/SELL/WAIT recommendation.

**Atlas never places trades.** It has no brokerage integration and no execution path.

> **Disclaimer.** Atlas was built for research and educational purposes. It is not financial
> advice, and using it — or acting on anything it recommends — is entirely at your own risk. Market
> data comes from Yahoo Finance's unofficial, unsupported API, which can be delayed, incomplete, or
> wrong. This project is open source under the [MIT License](LICENSE).

**Just want to use the dashboard?** Read [GETTING_STARTED.md](GETTING_STARTED.md) — no technical
background assumed. The rest of this page is for developers; [docs/](docs/) has the detail.

## What it does

For every symbol on the watchlist, every five minutes:

1. **Collects** hourly price bars from Yahoo Finance into TimescaleDB.
2. **Computes indicators** — RSI, EMA, MACD, ATR, ADX, OBV, Bollinger Bands, volume.
3. **Runs two ML models** — correction and recovery probability (XGBoost, trained in Python, run
   in Go via ONNX) — and classifies the market as BULL/BEAR/SIDEWAYS with a fixed rule.
4. **Reads the portfolio** — positions, P&L and portfolio-wide risk across all portfolios.
5. **Recommends** an action with a confidence and the reasons, and stores it.
6. **Notifies** Discord, Slack or Telegram when a symbol's action changes to BUY, ACCUMULATE,
   REDUCE or SELL.

Transactions can be in PLN, EUR or USD; each is converted to PLN at the NBP rate for its trade
date. The dashboard is in English, Polish, German, French and Spanish.

## Quickstart

Requires only Docker with Compose:

```bash
git clone <this repo>
cd atlas
docker compose up -d
```

Every setting has a working default. Open http://localhost:8080.

| Service | Address | Purpose |
|---|---|---|
| `atlas-api` | http://localhost:8080 | Dashboard and REST API |
| `timescaledb` | localhost:5432 | Database |
| `redis` | localhost:6379 | Cache for price bars and exchange rates |
| `prometheus` | http://localhost:9090 | Metrics |
| `alertmanager` | http://localhost:9093 | Infra alert routing |
| `grafana` | http://localhost:3000 | Dashboards (`admin`/`admin`) |
| `atlas-trainer` | internal only | Trains new model candidates on request from the Settings page |

**Every port is bound to `127.0.0.1`, and there is no login.** Anyone who can reach the dashboard
can read and change everything, so do not expose it beyond the machine it runs on.

For webhooks or a non-default database password, copy `.env.example` to `.env`. Every variable,
what persists and how to update: [docs/deployment.md](docs/deployment.md).

## How it is built

| Concern | Choice |
|---|---|
| Backend | Go, Clean Architecture (domain → application → infrastructure), one binary |
| Database | TimescaleDB; migrations applied at startup |
| Cache | Redis |
| Dashboard | Server-rendered `html/template`, Bootstrap 5.3 vendored, no JS build |
| ML training | Python — pandas, XGBoost, onnxmltools — in the `atlas-trainer` container; never in the `atlas-api` image |
| ML inference | Go, `onnxruntime_go` (cgo), so the image is Debian rather than Alpine |
| Monitoring | Prometheus, Grafana, Alertmanager |
| Deployment | Docker Compose, one machine, one person; images for `linux/amd64` and `linux/arm64` |

Every external dependency — market data, models, notification channels, exchange rates — is
optional: Atlas starts without it and says what is missing. See
[docs/architecture.md](docs/architecture.md), and [docs/adr/](docs/adr/) for why the load-bearing
decisions were made.

## Machine learning

- **Correction probability** — a drop within 10 hourly bars past 4× the instrument's own recent
  volatility (at least 2%).
- **Recovery probability** — given such a drop, whether the price gets back within 20 bars.

With no model loaded, correction falls back to a deterministic RSI-based stub and recovery stays
at 0. Every training run is judged on PR AUC against a no-features baseline (each symbol's own
historical rate), because a model can look accurate while only learning which symbols fall often.

The latest run (2026-09-24, 47 symbols): correction beats its baseline, PR AUC 0.183 against
0.128; recovery does not, 0.260 against 0.271.

Training writes new candidates and never activates them. A person picks the active model on the
Settings page, which shows each candidate's metrics and marks one that does not beat its
baseline. Details: [docs/ml.md](docs/ml.md), [training/README.md](training/README.md).

## Development

The Go toolchain is 1.27.2.

```bash
go build ./...
go vet ./...
gofmt -l .          # should print nothing
go test ./...
```

Three test groups skip without an environment variable: `TEST_DATABASE_DSN` (TimescaleDB),
`TEST_REDIS_ADDR` (Redis) and `TEST_ONNXRUNTIME_LIB_PATH` (a real `libonnxruntime`). Point the
first two at disposable containers, never at the database you use: those tests restore and flush
whole databases. CI sets all three.

The Python tests need Python 3.11, so run them in a container:

```bash
docker run --rm -v "$PWD:/app" -w /app python:3.11-slim-bookworm sh -c \
  'python -m venv /tmp/v && /tmp/v/bin/pip install -q -r training/requirements.txt pytest && /tmp/v/bin/python -m pytest -q -p no:cacheprovider training/'
```

The ONNX test fixtures come from `internal/application/ml/testdata/gen_smoketest.py`; re-run it
whenever `ml.FeatureOrder` changes, since the graph encodes the input width.

CI (`.github/workflows/ci.yml`) runs all of the above plus `govulncheck` and a Docker build on
every pull request. There is no deploy stage.

## Supported environment and known limitations

**Supported:** one person, on one machine, with Docker Compose; `linux/amd64` (verified) or
`linux/arm64` (builds and runs; model inference unverified); the service versions pinned in
`docker-compose.yml`.

**Deliberately not part of Atlas:** executing trades or any brokerage connection, logins or
multiple users, exposure beyond `127.0.0.1`, Kubernetes or any multi-host deployment, more than
one `atlas-api` against one database, and a separate SPA frontend.

**Known limitations:**

- The technical score has no measured selection value on 720 days of hourly bars
  ([docs/decision_engine.md](docs/decision_engine.md)); Atlas has no measured entry signal.
- The recovery model does not beat its baseline on the current dataset.
- Backups do not leave the machine and do not run on a schedule ([docs/runbook.md](docs/runbook.md)).
- Error messages that come from a provider or the database stay in English.
- The dashboard has been checked by hand; there is no automated browser, phone or keyboard test.

## Documentation

| Document | What it's for |
|---|---|
| [GETTING_STARTED.md](GETTING_STARTED.md) | Using the dashboard, no technical background assumed |
| [docs/deployment.md](docs/deployment.md) | Services, configuration, updates, monitoring |
| [docs/runbook.md](docs/runbook.md) | Backup, restore and recovery |
| [docs/architecture.md](docs/architecture.md) | Layers, modules and runtime |
| [docs/decision_engine.md](docs/decision_engine.md) | How a recommendation is scored, and what was measured |
| [docs/ml.md](docs/ml.md) | The models, their labels and their numbers |
| [docs/backtesting.md](docs/backtesting.md) | Historical simulation and what it can and cannot tell you |
| [docs/database.md](docs/database.md) | Tables, migrations, backup format |
| [docs/api.md](docs/api.md) | REST endpoints; interactive docs at `/swagger/index.html` |
| [training/README.md](training/README.md) | Training and activating models |
| [docs/adr/](docs/adr/) | Decisions that are expensive to reverse |
