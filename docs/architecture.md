# Architecture

Atlas is one Go binary (`cmd/api`) plus a training sidecar (`cmd/trainer`), run with Docker
Compose on one machine ([deployment.md](deployment.md)). This document is the as-built map; the
scoring rules live in [decision_engine.md](decision_engine.md), the models in [ml.md](ml.md), the
schema in [database.md](database.md), and the decisions behind the load-bearing parts in
[adr/](adr/).

## Layers

Clean Architecture, dependencies pointing inward only:

```
internal/app             HTTP handlers, dashboard, background loops, composition root (app.New)
  -> internal/application  use cases: collector, analyzer, ml, decision, portfolio, backtesting,
                           alerts, watchlist, exchangerate, notifications
    -> internal/domain       pure types and rules: market, portfolio, ml, decision, backtesting,
                             currency, watchlist, alerts
internal/infrastructure  adapters behind application interfaces: persistence (TimescaleDB),
                         cache (Redis), providers (Yahoo), exchangerate (NBP), notifications
                         (Discord/Slack/Telegram), trainer (client for cmd/trainer)
```

Business logic never lives in HTTP handlers. Every external dependency — market provider, ML
model, notification channel, exchange-rate source — sits behind an interface and is optional:
Atlas starts without it and says what it is missing, rather than refusing to boot.

## Modules

| Module | Package | What it does |
|---|---|---|
| Collector | `application/collector` | Fetches hourly OHLCV bars from Yahoo (the provider drops the unclosed live bar and holiday bars with no prices), upserts them into `prices`, caches them in Redis |
| Analyzer | `application/analyzer` | RSI, EMA, MACD, ATR, ADX, OBV, Bollinger Bands, volume |
| ML | `application/ml` | Builds the scale-free feature vector, runs the ONNX correction and recovery models, falls back to a deterministic stub; classifies market condition (BULL/BEAR/SIDEWAYS) with an ADX+EMA rule |
| Decision | `application/decision` | Technical score times ML, portfolio and risk factors, mapped to BUY/ACCUMULATE/HOLD/REDUCE/SELL/WAIT |
| Portfolio | `application/portfolio` | Transactions, positions rebuilt by replaying the ledger, P&L, portfolio-wide risk, up to 5 named portfolios |
| Exchange rates | `application/exchangerate` | Converts EUR/USD trades to PLN at the NBP rate for the trade date |
| Backtesting | `application/backtesting` | Historical simulation, ML evaluation, walk-forward threshold search |
| Alerts | `application/alerts`, `app/alerts_loop.go` | Background watchlist evaluation and change-only notifications through a durable outbox |
| Observability | `observability` | Prometheus metrics; logs are structured JSON via zap |

## Runtime

- **Background loop.** `internal/app/alerts_loop.go` evaluates the whole watchlist every
  `ALERT_POLL_INTERVAL_SECONDS` (default 300): refresh quotes, collect bars, score, persist,
  and queue notifications for symbols whose action changed. This is also what keeps price
  history accumulating for training.
- **One poller.** The loop runs only while this process holds the `leader_election` lease
  ([ADR 002](adr/002-one-leader-polls-not-every-replica.md)). Compose runs one `atlas-api`, so the
  lease mainly keeps an overlapping restart from polling twice.
- **Durable delivery.** Notifications are written to `alert_outbox` before anything sends them
  ([ADR 003](adr/003-alert-delivery-is-an-outbox.md)).
- **Database first.** Portfolio state is read from and written to TimescaleDB inside one
  transaction per change, never held in memory
  ([ADR 001](adr/001-database-is-authoritative-for-portfolio-state.md)).
- **Migrations** are embedded and applied at startup under an advisory lock.
- **Redis** caches price bars and exchange rates only; losing it costs refetches, not data.

## ML: train in Python, infer in Go

Training is offline Python (`training/`, pandas + XGBoost, exported to ONNX); serving is Go
(`onnxruntime_go`, cgo). The production image carries no Python. The feature vector is computed
by the same Go code on both sides — `cmd/export-training-data` calls `ml.BuildFeatureVector`, the
function live inference uses — so training and serving cannot drift apart on feature math.

Models are versioned files with a metadata sidecar holding their holdout metrics and label.
Training writes candidates; a person activates one from the Settings page. Nothing promotes a
model automatically. ML output is one input to the decision engine, never the decision itself.
