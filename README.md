# Atlas — portfolio decision support

Atlas watches a list of stock and ETF symbols, computes technical indicators,
runs ML models and combines them with your actual portfolio into an explained
BUY / ACCUMULATE / HOLD / REDUCE / SELL / WAIT recommendation. It is for one
person running it locally with Docker Compose. **Atlas never places trades**: it
has no brokerage integration and no execution path.

```text
$ curl -s 'localhost:8080/recommendations?symbol=MSFT' | jq -c '{symbol, action, confidence, market_condition, reason}'
{"symbol":"MSFT","action":"HOLD","confidence":0.3272436637908148,"market_condition":"BEAR","reason":["Mixed signal composition","Decision-support layer only"]}
```

- Every external dependency (market data, models, notification channels, exchange rates) is optional: Atlas starts without it and says what is missing.
- Training writes new model candidates and never activates them. You pick the active model on the Settings page, which marks any candidate that does not beat its baseline.
- With no model loaded, correction probability falls back to a deterministic RSI-based rule and recovery stays at 0.
- Every port is bound to `127.0.0.1`.

> **Disclaimer.** Atlas is for research and education. It is not financial
> advice; acting on it is at your own risk. Market data comes from Yahoo
> Finance's unofficial API, which can be delayed, incomplete or wrong.

## Install

Requires Docker with Compose. Runs on `linux/amd64`, and builds and runs on
`linux/arm64`.

```bash
git clone https://github.com/kmpoltorak/atlas.git
cd atlas
```

## Quick start

```bash
docker compose up -d
```

Every setting has a working default. Open http://localhost:8080, add symbols to
the watchlist and record your transactions. [GETTING_STARTED.md](GETTING_STARTED.md)
explains the dashboard with no technical background assumed.

Every five minutes, for each watched symbol, Atlas collects hourly bars from
Yahoo Finance, computes RSI, EMA, MACD, ATR, ADX, OBV, Bollinger Bands and
volume, runs the correction and recovery models, classifies the market as
BULL / BEAR / SIDEWAYS, reads positions and risk across all portfolios, and
stores a recommendation. When a symbol's action changes to BUY, ACCUMULATE,
REDUCE or SELL, it notifies Discord, Slack or Telegram. Transactions can be in
PLN, EUR or USD; each is converted to PLN at the NBP rate for its trade date.
The dashboard is in English, Polish, German, French and Spanish.

## Services

| Service | Address | Purpose |
|---|---|---|
| `atlas-api` | http://localhost:8080 | dashboard and REST API |
| `timescaledb` | localhost:5432 | database; migrations run at startup |
| `redis` | localhost:6379 | cache for price bars and exchange rates |
| `prometheus` | http://localhost:9090 | metrics |
| `alertmanager` | http://localhost:9093 | infra alert routing |
| `grafana` | http://localhost:3000 | dashboards (`admin` / `admin`) |
| `atlas-trainer` | internal only | trains model candidates on request from the Settings page |

## Configuration

Copy `.env.example` to `.env`; Compose reads it automatically.

| Variable | Default | Meaning |
|---|---|---|
| `DATABASE_PASSWORD` | `atlas` | used by both `atlas-api` and `timescaledb` |
| `GRAFANA_ADMIN_PASSWORD` | `admin` | Grafana admin password |
| `DISCORD_WEBHOOK_URL`, `SLACK_WEBHOOK_URL` | empty | recommendation-change alerts; any combination |
| `TELEGRAM_BOT_TOKEN`, `TELEGRAM_CHAT_ID` | empty | same, via Telegram |
| `ALERTMANAGER_DISCORD_WEBHOOK_URL` | empty | separate channel for infra alerts and training failures |
| `ML_ENABLED` | `true` | startup default for blending ML, portfolio and risk into the score; the Settings page overrides it live |
| `MODEL_PATH`, `RECOVERY_MODEL_PATH` | empty | ONNX models under `training/models` |
| `TRAINER_URL` | `http://atlas-trainer:8091` | empty hides the train and delete buttons |
| `REDIS_PASSWORD` | empty | sets both the Redis server and the client |
| `DERIVED_RETENTION_DAYS` | `0` (keep all) | days of indicators, recommendations and predictions to keep |
| `DATABASE_MAX_OPEN_CONNS`, `DATABASE_MAX_IDLE_CONNS`, `DATABASE_CONN_MAX_LIFETIME_MINUTES` | 20, 5, 30 | connection pool |

What persists, how to update and how to monitor: [docs/deployment.md](docs/deployment.md).
Backup and restore: [docs/runbook.md](docs/runbook.md). The REST API is listed in
[docs/api.md](docs/api.md) and served interactively at `/swagger/index.html`.

**Models.** Correction probability predicts a drop within 10 hourly bars past 4×
the instrument's recent volatility (at least 2%); recovery probability predicts
whether the price gets back within 20 bars. Each run is judged on PR AUC against
a per-symbol base-rate baseline. The latest run (2026-09-24, 47 symbols):
correction 0.183 against 0.128, recovery 0.260 against 0.271. Training and
activation: [training/README.md](training/README.md), details: [docs/ml.md](docs/ml.md).

## Limitations

- No login. Anyone who can reach the dashboard can read and change everything, so do not expose it beyond the machine.
- One person, one machine: no multiple users, no multi-host or Kubernetes deployment, and no more than one `atlas-api` per database.
- The technical score has no measured selection value on 720 days of hourly bars ([docs/decision_engine.md](docs/decision_engine.md)): Atlas has no measured entry signal.
- The recovery model does not beat its baseline on the current dataset.
- Model inference on `linux/arm64` is not verified.
- Backups do not leave the machine and do not run on a schedule.
- Error messages from a provider or the database stay in English.
- The dashboard has been checked by hand only: no automated browser, phone or keyboard tests.

## Development

There is no Makefile; Go 1.27.2:

```bash
go build ./...
go vet ./...
gofmt -l .          # prints nothing
go test ./...
```

Three test groups skip unless an environment variable is set:
`TEST_DATABASE_DSN` (TimescaleDB), `TEST_REDIS_ADDR` (Redis) and
`TEST_ONNXRUNTIME_LIB_PATH` (a real `libonnxruntime`). Point the first two at
disposable containers only, because those tests restore and flush whole
databases. The Python training tests need Python 3.11:

```bash
docker run --rm -v "$PWD:/app" -w /app python:3.11-slim-bookworm sh -c \
  'python -m venv /tmp/v && /tmp/v/bin/pip install -q -r training/requirements.txt pytest && /tmp/v/bin/python -m pytest -q -p no:cacheprovider training/'
```

Re-run `internal/application/ml/testdata/gen_smoketest.py` whenever
`ml.FeatureOrder` changes, since the ONNX fixtures encode the input width. On
every pull request CI sets all three variables, runs `go test -race`, the
training split tests, `govulncheck`, a Docker build and a trainer container
smoke test.

Built with an AI coding assistant. I wrote the spec and reviewed every change.

## License

MIT, see [LICENSE](LICENSE).
