# Atlas AI Portfolio Advisor - Project Context

## Project Summary

Atlas is an AI-powered portfolio decision support system for stocks and ETFs.

The purpose of Atlas is to analyze financial markets, monitor user portfolios, detect opportunities and risks, and generate explainable investment suggestions.

Atlas is NOT an automated trading bot.

Atlas does NOT execute trades.

Atlas provides analytical recommendations only.

The system should help answer:

- Should I buy?
- Should I accumulate?
- Should I hold?
- Should I reduce my position?
- Should I sell?
- Should I wait?

Every recommendation must include:

- action,
- confidence score,
- explanation,
- supporting indicators,
- risk information.

---

# Core Architecture

Atlas is a modular backend system: Collector (market data), Analyzer (technical indicators),
ML Engine (correction/recovery probability + market condition), Portfolio Engine (positions,
P&L, multi-currency, risk), Decision Engine (blends all of the above into
BUY/ACCUMULATE/HOLD/REDUCE/SELL/WAIT with a confidence score and explanation), Notification
Service (Discord/Slack/Telegram), and Metrics (Prometheus/Grafana).

Full per-module responsibilities and implementation status live in one place now, not two:
[docs/architecture.md](docs/architecture.md) covers every module above; two of them go deeper
still in their own doc — [docs/decision_engine.md](docs/decision_engine.md) (the full scoring
model and action-selection rules) and [docs/ml.md](docs/ml.md) (the training pipeline and real
measured model performance). This section used to duplicate that detail here as well, drifting
out of sync with it as the project grew — kept as a short summary only, on purpose.

---

# Technology Stack

## Production Backend

Language:

Go

Go is responsible for:

- REST API,
- market data collection,
- technical analysis,
- portfolio management,
- decision engine,
- notifications,
- metrics,
- orchestration,
- web dashboard.

Implementation status: the web dashboard (`internal/app/web`) is real — server-rendered
`html/template`, no separate frontend build/deploy, styled with Bootstrap 5.3 (vendored locally,
not a CDN or npm build step) with light/dark/auto theming — see implementation-plan.md's Phase 8
status note for the full scope (portfolio/recommendations/alerts/backtests/watchlist pages,
single-account login with password + TOTP 2FA enrolled via a real in-app QR code, opt-in — no
auth gate at all when unconfigured). The watchlist page also exports and imports the whole list
as a JSON file (`GET /watchlist/export`, `POST /watchlist/import`, plus a file-upload form), so a
list built up in one environment moves to another without re-entering several dozen symbols by
hand. Import defaults to merging; `mode=replace` mirrors the file exactly and is the only way to
remove anything, so the destructive behaviour always has to be asked for by name.

---

## Machine Learning Architecture

ML is a dedicated part of Atlas architecture.

Programming languages responsibility:

---

## Go

Go is the primary production language.

Go is responsible for:

- loading trained models,
- generating predictions,
- integrating predictions into Decision Engine,
- exposing ML metrics.

The production system must not depend on Python runtime.

---

## Python

Python is used only for Machine Learning workflows.

Python responsibilities:

- dataset preparation,
- feature engineering experiments,
- exploratory analysis,
- model training,
- model validation,
- hyperparameter optimization,
- model evaluation.

Initial ML model:

XGBoost

The ML component must expose a stable interface.

Possible communication:

- exported model format,
- REST API,
- gRPC.

The rest of the system must not depend directly on Python implementation details.

ML can be replaced without changing business logic.

---

# Market Data Providers

Initial market data source:

## Yahoo Finance

Used for MVP.

Reasons:

- broad stock coverage,
- ETF support,
- historical data availability.

Important:

Yahoo Finance does not provide an official API for all use cases.

Confirmed: developer.yahoo.com's OAuth2 platform covers only Oath Ad Platforms and
UserInfo APIs. It does not cover Yahoo Finance or any market data product — there is no
official Yahoo Finance API to authenticate against, free or paid. The provider calls the
unofficial query2.finance.yahoo.com chart endpoint; it requires a browser-like User-Agent
header or it returns HTTP 429 regardless of query parameters (this was the root cause of
the 429s observed during testing, not the endpoint itself). It works once that header is
set, but remains unofficial and could change or be blocked without notice.

Bars are hourly (`interval=1h`), not daily — daily candles are too coarse for the
trading-oriented use this engine targets. Confirmed live: a real 30-day request returns
clean hourly bars with 1-hour gaps during market hours, well inside Yahoo's ~730-day
retention for intraday data (see internal/infrastructure/providers/yahoo.go).

Yahoo-specific logic must be isolated inside the provider module.

---

## Alternative Provider

## Stooq

Evaluated as a free alternative, then removed. Its free endpoint served a client-side
JavaScript proof-of-work bot challenge instead of CSV data from every network tested — and
even where reachable, its free CSV export is daily-only (no intraday), which wouldn't have
met the hourly-granularity requirement anyway. Yahoo (unofficial) is now the sole provider;
`MARKET_PROVIDER` still exists as a metrics/logging label (see internal/app/handlers.go),
just not as a provider selector, since there's nothing left to select.

---

## Exchange Rates (NBP)

Real: `internal/infrastructure/exchangerate.NBPProvider` fetches Table A (the average/mid
reference rate) from `https://api.nbp.pl/api/exchangerates/tables/A/`, NBP's real, free, keyless
API — used to convert non-PLN portfolio transactions to PLN (see Portfolio Engine section above).
Like Yahoo, NBP's endpoint returns HTTP 403 for Go's default `Go-http-client/*` User-Agent
specifically (confirmed live — curl with the same UA also gets 403); a bare, generic
`Mozilla/5.0` override is enough and is deliberately all this sends, no app-identifying details.
NBP itself only republishes Table A once per business day (~11:45 CET) — Atlas's hourly Redis
cache bounds how often it calls out, not how fresh the underlying rate can be.

## Future Providers

The architecture should support:

- Alpha Vantage,
- Twelve Data,
- Finnhub,
- Polygon.

Provider implementations must be replaceable without changing business logic.

---

# Database

TimescaleDB (prices as a real hypertable; symbols/portfolios/positions/transactions/watchlists/
predictions/recommendations/backtest runs as plain relational tables) plus Redis (short-lived
bars cache, session store). Full per-table detail and implementation status, kept as one living
copy instead of two: [docs/database.md](docs/database.md).

---

# Architecture Rules

Always follow:

- Clean Architecture,
- SOLID principles,
- modular design,
- dependency injection,
- interfaces,
- separation of concerns.

Never:

- put business logic in HTTP handlers,
- hardcode configuration,
- create hidden dependencies,
- duplicate functionality,
- bypass existing abstractions.

---

# Development Rules

Before coding:

1. Read project documentation.
2. Understand existing architecture.
3. Explain the planned changes.
4. Identify affected modules.
5. Implement incrementally.

The project documentation is the source of truth.

Do not invent missing requirements.

Do not replace technologies without explicit approval.

---

# Important Constraints

Atlas is:

- an analytical assistant,
- an explainable AI platform,
- a portfolio decision support system.

Atlas is NOT:

- a trading bot,
- a price prediction oracle,
- an automatic investment system.

All recommendations must be explainable and include uncertainty.