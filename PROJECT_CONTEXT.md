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

Atlas is a modular backend system.

Main components:

## Collector

Responsible for:

- downloading market data,
- normalizing external provider data,
- storing historical prices.

The Collector uses abstract provider interfaces.

The system must not depend directly on one market data provider.

---

## Analyzer

Responsible for:

- technical analysis,
- calculating indicators,
- generating market features.

Indicators include:

- RSI
- EMA
- MACD
- ATR
- ADX
- Volume analysis
- Volatility metrics

Implementation status: RSI, EMA, MACD, ATR, and volume analysis are implemented
(internal/application/analyzer). ADX and broader volatility metrics are not yet implemented.

---

## ML Engine

Responsible for machine learning predictions.

The ML Engine predicts:

- correction probability,
- recovery probability,
- market condition,
- confidence score.

ML predictions are supporting signals.

ML is NOT the final decision maker.

Implementation status: the ML prediction path is a deterministic stub (internal/application/ml)
— it returns a score derived from RSI plus a fixed confidence, not a trained model, and has no
correction/recovery-probability or market-condition fields yet. No training pipeline exists.

---

## Portfolio Engine

Responsible for:

- tracking owned positions,
- storing buy prices,
- calculating profit/loss,
- calculating exposure,
- portfolio risk analysis.

The Decision Engine must always consider portfolio context.

---

## Decision Engine

The Decision Engine is the main business intelligence component.

It combines:

- technical analysis,
- ML predictions,
- portfolio state,
- risk analysis,
- market conditions.

It produces:

- BUY
- ACCUMULATE
- HOLD
- REDUCE
- SELL
- WAIT

Every recommendation must explain WHY.

Implementation status: the current decision engine (internal/application/decision) only
combines technical analysis into a score; ML and portfolio state are not yet read by it
(Orchestrator.Portfolio is wired but unused). It only ever emits BUY/SELL/HOLD — ACCUMULATE,
REDUCE, and WAIT are not implemented.

---

## Notification Service

Responsible for:

- sending important alerts,
- delivering recommendations.

Initial integration:

- Discord Webhooks

---

## Metrics

Everything important must be observable.

Technology:

- Prometheus
- Grafana

Metrics include:

- system metrics,
- provider metrics,
- database metrics,
- ML metrics,
- decision metrics.

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
- orchestration.

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

Yahoo-specific logic must be isolated inside the provider module.

---

## Alternative Provider

## Stooq

Evaluate as a free alternative.

Use Stooq if:

- required instruments are available,
- data quality is sufficient,
- historical coverage meets requirements.

Decision: Yahoo (unofficial) remains the default provider, confirmed working end-to-end
with a browser-like User-Agent header. Stooq is implemented and available via
MARKET_PROVIDER=stooq, but its endpoint was observed serving a client-side JavaScript
proof-of-work bot challenge instead of CSV data from at least one test network — Atlas
does not attempt to solve that challenge. This may be network/IP-specific; verify Stooq
access from the actual deployment host before switching to it as the default.

---

## Future Providers

The architecture should support:

- Alpha Vantage,
- Twelve Data,
- Finnhub,
- Polygon.

Provider implementations must be replaceable without changing business logic.

---

# Database

Primary database:

TimescaleDB

Used for:

- historical prices,
- indicators,
- portfolio data,
- ML predictions,
- recommendations,
- alerts.

Implementation status: prices and portfolio data (positions, transactions, watchlist) are
persisted (see migrations/001-003). Indicators, ML predictions, and recommendations are
computed fresh per request and not yet written to TimescaleDB — their tables exist in
migrations/001 but nothing inserts into them yet. There is no alerts table at all yet
(alerts are computed on demand from the watchlist, see internal/application/alerts).

Cache:

Redis

Used for:

- temporary calculations,
- API caching,
- rate limiting.

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