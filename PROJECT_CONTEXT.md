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
all implemented (internal/application/analyzer). RSI/EMA/MACD/ATR/Volume/OBV/Bollinger Bands are
all wired into the decision engine's scoring (Orchestrator.buildTechnicalScore blends momentum,
RSI, a Bollinger %B position signal, and an OBV accumulation/distribution signal as a weighted
average — see docs/decision_engine.md); ADX is wired into market-condition classification
(`ml.ClassifyMarketCondition`, ADX+EMA — see docs/ml.md) instead. `ml.FeatureVector` (the trained
ONNX model's input contract) holds eight scale-free features rather than raw price-unit values:
`rsi, ema_dist, macd_pct, tr_pct, atr_pct, rvol_20, rvol_50, volume`. Two analyzer methods exist
solely for it — `ComputeSmoothedATR` (the genuine Wilder ATR; `ComputeATR` returns raw per-bar
true range despite its name, and is left that way because `/prices`, the decision engine and the
persisted indicators table all consume it) and `ComputeRealizedVolatility`. Adding that volatility
family was the single largest model improvement the project has made — see docs/ml.md. Extending
the feature set further remains the most promising lead for the correction model, which still
trails the base-rate baseline on ROC AUC even while beating it on PR AUC. Broader volatility/trend
*summary* metrics (e.g. a single consolidated risk/trend score) are still not implemented — only
the individual indicator series are.

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
normal non-fatal state, not an error. Recovery probability is real now too — a second,
independently-optional model (`RECOVERY_MODEL_PATH`, its own `OnnxModel`) trained on
`ml.ComputeRecoveryLabel`'s real, tested label definition ("did a correction recover to its
pre-drop price within N bars"), verified live end-to-end the same way (persisted
`recovery_probability`/`recovery_model_version` — migrations/010).

Where both models actually stand, measured rather than assumed: on the current dataset (10,002
correction / 1,036 recovery rows across 47 symbols) **both beat the per-symbol base-rate baseline
on PR AUC** — correction 0.264 vs 0.188, recovery 0.851 vs 0.651 — which is the metric promotion
is judged on, since Atlas alerts on the top of the ranking rather than the uneventful middle. At
its 25 highest-scoring bars the correction model was right 68% of the time against a 6.5% base
rate (10.5x lift; the same alert budget on the baseline returned 16%). It reached that after
gaining three volatility features (`atr_pct`, `rvol_20`, `rvol_50`) — volatility being exactly
what the pre-fix model had been reaching for when it memorised symbol identity. Note it still
trails the baseline on ROC AUC (0.710 vs 0.748); the two metrics genuinely disagree, and PR AUC
is the one that matches what the product does. This all follows a real fix: the feature vector originally fed raw price-unit EMA/MACD/ATR to the model,
which let it identify the symbol instead of reading its dynamics, and the reported accuracy of
0.919 was worse than always predicting "no correction" (0.935). Features are now scale-free
ratios, the train/holdout split has an embargo gap, and `train.py` computes the base-rate
baseline on every run so a model that only memorises symbols can't look good again — it prints a
warning, records it in the metadata sidecar, and the Account page's picker labels it "DOES NOT
BEAT BASELINE". Full detail and the numbers behind each claim: docs/ml.md.

Retraining and promotion no longer require editing an env var
and restarting: `training/retrain.sh` generates new, uniquely-versioned candidates on demand or a
schedule, and the Account dashboard page (`/dashboard/account`) lets an admin pick which trained
model is active per role from real holdout metrics — a hot-swap (`ml.UseCase.SetModel`/
`SetRecoveryModel`, `atomic.Pointer`) with no restart, durably recorded so it survives one. A
human still decides which candidate goes live; nothing auto-promotes (see docs/ml.md's "Model
selection and retraining" section). Market condition is real and live today, but deliberately
*not* ML — `ml.ClassifyMarketCondition` is a deterministic ADX+EMA technical rule
(BULL/BEAR/SIDEWAYS), which doesn't share either probability model's data-volume concerns, wired
into `Recommendation.MarketCondition` (see Decision Engine section below).

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
Portfolio Engine's own section below). Risk analysis (`portfolio.UseCase.ComputeRisk`) is real
and exposed via `GET /portfolio`'s response (`risk` field alongside `positions`). Its Drawdown
field is a current-state proxy (unrealized loss across losing positions, as a fraction of
exposure), not a true historical peak-to-trough max drawdown — that needs a portfolio equity
curve over time, which needs snapshot persistence (not implemented). The Decision Engine now
does consume this risk snapshot in its scoring too (`computeRiskScore`, weight 0.10 — see
docs/decision_engine.md), alongside `portfolioRiskSignal` (P&L-based, a separate, narrower,
symbol-specific heuristic that's still its own component, weight 0.15) — the two aren't the same
signal and both contribute independently. Multi-currency is real too: `Transaction.Currency`
(PLN/EUR/USD — see internal/domain/currency) is converted to a PLN `PriceInPLN` at the moment
it's recorded (via `internal/application/exchangerate`, backed by NBP Table A — see docs/
deployment.md), and `Position.AverageCost`/`CurrentPrice` are always in PLN regardless of what
currency the underlying transactions were entered in, so a symbol bought partly in USD and
partly in PLN still has one mathematically coherent cost basis. Everything downstream (this risk
calc, ROI, Decision Engine scoring, backtesting) reads plain PLN numbers and never needs to know
multi-currency exists — only the recording/display layer does. `CurrentPrice` specifically is
not a live market quote — `applyTransaction` sets it once, to the most recently recorded
transaction's price for that symbol (BUY or SELL), and it stays there until the next
transaction; the dashboard/portfolio pages label it "Last Transaction Price" with an "as of
`<date>`" line for exactly this reason, not "Current Price".

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
technical analysis, ML prediction, per-symbol portfolio P&L, and portfolio-wide risk analysis
(Exposure/Drawdown/Volatility, via `portfolio.UseCase.ComputeRisk`) into its score — the full
four-term "Scoring Model" below is real now, not just Technical+ML+Portfolio (see
docs/decision_engine.md and docs/ml.md — ML is a real trained model when one is loaded, falling
back to a deterministic stub otherwise), though the blend weights (0.50/0.25/0.15/0.10) are
still placeholders. `Recommendation.RiskScore` is a real field now too (previously always NULL
in the `recommendations` table — see migrations/001, docs/decision_engine.md's Database
section). `Recommendation.MarketCondition` (BULL/BEAR/SIDEWAYS) is also real and persisted
(migrations/007) — a deterministic ADX+EMA technical rule, not part of the score blend itself,
just reported alongside it (see docs/ml.md). `Recommendation.MLModelVersion` (distinct from
`ModelVersion`, the decision engine's own constant tag) surfaces which real ML model actually
produced `MLScore`, persisted too (migrations/012) — previously there was no way to see this
outside `/predictions`. `Confidence` was a fixed literal per action band until a later pass (every
HOLD reported 0.6 regardless of score strength, which is most real recommendations); it now
scales with how far the score sits from the neutral center, matching how ML's own confidence is
already computed — see docs/decision_engine.md's Implementation status note for the full story.
All six
actions above are now real, differentiated by real signals, not cosmetic renaming:
- BUY vs ACCUMULATE, SELL vs REDUCE: gated on whether a real portfolio position already exists
  (Portfolio.GetPositions), independent of that position's P&L.
- SELL vs REDUCE: score severity — a strongly bearish blended score (<=0.25) with a position is
  SELL; a moderately bearish one (0.25-0.45] with a position is REDUCE.
- HOLD vs WAIT: a genuinely mixed score (0.45-0.55) is always HOLD; a score that leans one
  direction (0.55-0.75 bullish, or the bearish bands above without a position to act on) is WAIT
  when the ML model's confidence is below 0.30, else HOLD.
All thresholds are first-pass, documented heuristics, same honesty stance as the original
BUY>=0.75/SELL<=0.4 cutoffs they replace — but no longer hardcoded constants: they're a
`decision.Thresholds` value (internal/domain/decision/types.go) on `decision.UseCase`, defaulting
to the same cutoffs everywhere in live traffic. This is what makes `GET /backtests/walkforward`
possible — a small grid search around the defaults, evaluated against historical data and
reported back for manual review, never auto-applied to the live engine (see
docs/backtesting.md's "Optimization" section). Backtesting previously reused the live Portfolio
use case for HasPosition instead of its own simulated position — fixed, see docs/backtesting.md's
Implementation status note.

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

Implementation status: `/metrics` (Prometheus format, see `internal/observability/metrics.go`)
exposes HTTP request/duration, provider, decision, ML, Discord-alert and alert-poll-error
counters plus an `atlas_dependency_up{dependency="…"}` gauge (refreshed every 15s from the same
checks `/health` runs), scraped by Prometheus (`prometheus/prometheus.yml`) and visualized in
Grafana (`grafana/dashboards/`). The optional `atlas-trainer` sidecar is a second scrape job with
its own separate registry (`atlas_trainer_runs_total`/`_run_duration_seconds`/
`_last_run_timestamp_seconds`), so a training run's outcome and staleness are graphable rather
than only visible in a point-in-time `/status` response.
Infrastructure-level alerting rules — distinct from the Discord alerts `internal/app/
alerts_loop.go` sends for recommendation changes — now exist too (`prometheus/alerts.yml`:
API-down, crash-loop/flapping, high error rate), routed through Alertmanager
(`alertmanager/alertmanager.yml`) to a real, separate Discord channel (webhook URL set via the
gitignored `.env`, never committed); see `deploy/RUNBOOK.md`'s "Infrastructure alerting" section
for the alert list, the real upstream Alertmanager `v0.27.0` bug this surfaced (fixed by pinning
`v0.28.1` instead), and how to verify it live.

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
auth gate at all when unconfigured).

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
predictions, recommendations, backtest runs, and now indicators are all persisted (see
migrations/001-006,
internal/infrastructure/persistence/{portfolio,watchlist,decision,ml,backtesting,indicators}.go).
Indicators are still computed fresh per request everywhere (no caching) — persistence is a
side-effect write, not a read path — and every live-recommendation path now triggers it
(`GET /recommendations`, `GET /alerts`, and the background alert-poll loop); backtesting
deliberately still doesn't, since its bars are historical/simulated rather than live readings
(see docs/deployment.md's Implementation status note). Predictions always populate `score`/`confidence`/`model_version`; `correction_probability`
is populated too when a real trained model produced it (see docs/ml.md), and stays NULL for the
deterministic stub. `expected_duration`/`expected_strength` stay NULL regardless — no model
produces those yet. Backtesting's individual simulated recommendations are still not persisted
one-by-one (they're hypothetical, not real, decisions), but each full run's summary (returns,
trades, ML evaluation) now is — append-only in `backtest_runs`, queryable via
`GET /backtests/history` — see docs/backtesting.md. There is no alerts table at all yet (alerts
are computed on demand from the watchlist, see internal/application/alerts).

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