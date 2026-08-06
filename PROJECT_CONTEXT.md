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

Implementation status: RSI, EMA, MACD, ATR, volume analysis, OBV, ADX, and Bollinger Bands are
all implemented (internal/application/analyzer). None of these are wired into the decision
engine's scoring yet beyond what was already there (RSI, momentum/volume) — they're available,
tested analyzer capabilities, not yet blended into Orchestrator.buildTechnicalScore or
ml.FeatureVector (see docs/decision_engine.md). Broader volatility/trend *summary* metrics
(e.g. a single consolidated risk/trend score) are still not implemented — only the individual
indicator series are.

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

Implementation status: a real training pipeline exists (training/, see training/README.md) —
an XGBoost binary classifier predicting correction probability, trained on real collected OHLCV
history, exported to ONNX and loaded in Go via `github.com/yalue/onnxruntime_go`
(internal/application/ml, `OnnxModel`) — verified live end-to-end (train → export → real HTTP
prediction → persisted `correction_probability` row). No model is loaded by default; unset
`MODEL_PATH` (or a load failure) falls back to the original deterministic RSI-derived stub, a
normal non-fatal state, not an error. Recovery probability and market-condition fields still
don't exist — only correction probability and confidence are real outputs today.

---

## Portfolio Engine

Responsible for:

- tracking owned positions,
- storing buy prices,
- calculating profit/loss,
- calculating exposure,
- portfolio risk analysis.

The Decision Engine must always consider portfolio context.

Implementation status: positions/buy-prices/P&L/exposure are all real and persisted (see
Portfolio Engine's own section below). Risk analysis (`portfolio.UseCase.ComputeRisk`) exists
with real logic but was dead code until now — nothing called it. It's now exposed via
`GET /portfolio`'s response (`risk` field alongside `positions`). Its Drawdown field is a
current-state proxy (unrealized loss across losing positions, as a fraction of exposure), not a
true historical peak-to-trough max drawdown — that needs a portfolio equity curve over time,
which needs snapshot persistence (not implemented). The Decision Engine does not consume this
risk snapshot in its scoring — portfolio context there comes from `portfolioRiskSignal` (P&L-based,
a separate, narrower heuristic — see docs/decision_engine.md), not `ComputeRisk`. Wiring
`ComputeRisk` into scoring would mean designing real weighting rules first (the same caution
applied to ACCUMULATE/REDUCE/WAIT), not done here.

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

Implementation status: the current decision engine (internal/application/decision) now blends
technical analysis, ML prediction, and portfolio risk context into its score (see
docs/decision_engine.md and docs/ml.md — ML is a real trained model when one is loaded, falling
back to a deterministic stub otherwise), though the blend weights are placeholders. All six
actions above are now real, differentiated by real signals, not cosmetic renaming:
- BUY vs ACCUMULATE, SELL vs REDUCE: gated on whether a real portfolio position already exists
  (Portfolio.GetPositions), independent of that position's P&L.
- SELL vs REDUCE: score severity — a strongly bearish blended score (<=0.25) with a position is
  SELL; a moderately bearish one (0.25-0.45] with a position is REDUCE.
- HOLD vs WAIT: a genuinely mixed score (0.45-0.55) is always HOLD; a score that leans one
  direction (0.55-0.75 bullish, or the bearish bands above without a position to act on) is WAIT
  when the ML model's confidence is below 0.30, else HOLD.
All thresholds are first-pass, documented heuristics (internal/application/decision/usecase.go),
same honesty stance as the original BUY>=0.75/SELL<=0.4 cutoffs they replace. A real caveat:
backtesting reuses the live Portfolio use case for HasPosition, not its own simulated position
— see docs/backtesting.md's Implementation status note.

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

Implementation status: prices, portfolio data (positions, transactions, watchlist), ML
predictions, and recommendations are all persisted (see migrations/001-004,
internal/infrastructure/persistence/{portfolio,watchlist,decision,ml}.go). Indicators are
still computed fresh per request rather than persisted — there is no indicators table write
path. Predictions always populate `score`/`confidence`/`model_version`; `correction_probability`
is populated too when a real trained model produced it (see docs/ml.md), and stays NULL for the
deterministic stub. `expected_duration`/`expected_strength` stay NULL regardless — no model
produces those yet. Backtesting's simulated recommendations are deliberately not persisted.
There is no alerts table at all yet (alerts are computed on demand from the watchlist, see
internal/application/alerts).

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