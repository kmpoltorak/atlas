# Atlas — Project Context

Scope and constraints. Everything about *how* it is built lives in [docs/](docs/); the standing
rules the code is held to live in [CLAUDE.md](CLAUDE.md). This file is deliberately short — it used to
restate the per-module architecture and drifted out of sync with the docs that own it.

## What Atlas is

An AI-powered decision-support system for a personal stock/ETF portfolio. It analyzes market data,
detects market conditions, estimates correction and recovery probability, reads the actual
portfolio, and answers one question per symbol: buy, accumulate, hold, reduce, sell, or wait.

Every recommendation carries an action, a confidence score, an explanation, the indicators that
contributed, and risk context. A bare action is not a shippable output.

## What Atlas is not

Not a trading bot, not a price-prediction oracle, not an automatic investment system. **It never
executes trades** — no brokerage integration exists, and adding one is a separate design
conversation, not an increment. Recommendations are explainable and carry their uncertainty.

## Core architecture

A modular Go backend: Collector (market data), Analyzer (technical indicators), ML Engine
(correction/recovery probability + market condition), Portfolio Engine (positions, P&L,
multi-currency, risk), Decision Engine (blends all of the above into
BUY/ACCUMULATE/HOLD/REDUCE/SELL/WAIT with a confidence score and explanation), Notification
Service (Discord/Slack/Telegram), and Metrics (Prometheus/Grafana). The web dashboard is
server-rendered `html/template` — no separate frontend stack.

Per-module responsibilities and implementation status:
[docs/architecture.md](docs/architecture.md). Two modules go deeper in their own docs —
[docs/decision_engine.md](docs/decision_engine.md) (the scoring model and action-selection rules)
and [docs/ml.md](docs/ml.md) (the training pipeline and real measured model performance).
Decisions that are expensive to reverse: [docs/adr/](docs/adr/).

## Market data providers

This section is here rather than in `docs/` because it records what was actually found by calling
these endpoints, not how the code is shaped.

**Yahoo Finance** is the sole provider, via the unofficial `query2.finance.yahoo.com` chart
endpoint. There is no official alternative: developer.yahoo.com's OAuth2 platform covers only Oath
Ad Platforms and UserInfo, no market data product, free or paid. The endpoint requires a
browser-like `User-Agent` or it returns HTTP 429 regardless of query parameters — that header, not
the endpoint, was the root cause of the 429s seen during testing. It remains unofficial and could
change or be blocked without notice, so Yahoo-specific logic stays isolated inside the provider
module.

Bars are hourly (`interval=1h`), not daily: daily candles are too coarse for the trading-oriented
use this engine targets. Confirmed live — a 30-day request returns clean hourly bars, well inside
Yahoo's ~730-day intraday retention (`internal/infrastructure/providers/yahoo.go`).

**Stooq** was evaluated as a free alternative and removed. Its free endpoint served a client-side
JavaScript proof-of-work bot challenge instead of CSV from every network tested, and even where
reachable its free export is daily-only. `MARKET_PROVIDER` survives as a metrics/logging label,
not a provider selector — there is nothing left to select.

**NBP** supplies exchange rates: Table A (the average/mid reference rate) from
`https://api.nbp.pl/api/exchangerates/tables/A/`, a real, free, keyless API, used to convert
non-PLN transactions to PLN. Like Yahoo it returns HTTP 403 for Go's default `Go-http-client/*`
User-Agent specifically (confirmed live; curl with the same UA also gets 403); a bare `Mozilla/5.0`
is enough, and is deliberately all that is sent. NBP republishes Table A once per business day
(~11:45 CET), so Atlas's hourly Redis cache bounds how often it calls out, not how fresh the rate
can be.

Any future provider must be replaceable without changing business logic.

## Database

TimescaleDB — `prices` as a real hypertable, everything else (symbols, portfolios, positions,
transactions, watchlists, predictions, recommendations, backtest runs) as plain relational tables
— plus Redis for the short-lived bars cache and the session store. Per-table detail:
[docs/database.md](docs/database.md); the authoritative schema is `migrations/*.sql`.
