# Backtesting Engine

**Implementation status**: internal/application/backtesting implements a real simulation (no
future *bar* data leakage, verified by regression test) of exactly one fixed all-in/all-out
strategy driven by the live Decision Engine — not the pluggable Strategy interface described
below, and no other example strategies exist. It computes Returns, Max Drawdown, Volatility,
Sharpe Ratio, and Win Rate. ML Evaluation (Accuracy/Precision/Recall/F1/ROC AUC — see
`UseCase.evaluateML`), persistence (`backtest_runs`, see below), and now walk-forward
train/validate/test splitting with threshold optimization (`GET /backtests/walkforward`, see
"Walk Forward Testing"/"Optimization" below) are all real.

**Portfolio-state leakage — fixed**: backtesting previously reused the exact same Orchestrator
instance as live recommendations (see internal/app/app.go), so every simulated
recommendation's `decision.RecommendationInput.HasPosition` (see docs/decision_engine.md's
ACCUMULATE/REDUCE/WAIT differentiation) reflected the **live, real, present-day** portfolio, not
the backtest's own simulated position built up bar-by-bar. `UseCase.Run` now constructs its own
`portfolio.UseCase` (`simPortfolio`, no Repository — never touches TimescaleDB) and its own
Orchestrator around it (`simOrchestrator`, reusing the live Analyzer/Decision/ML, which are
stateless with respect to any specific portfolio), syncing `simPortfolio.Positions` to the
simulation's own quantity/entry price before every bar's recommendation. A backtest's action
sequence (BUY vs. ACCUMULATE, SELL vs. REDUCE) is now independent of whatever the live portfolio
holds — see `TestRunDoesNotLeakLivePortfolioState`.

**ML Evaluation**: when a real trained model is loaded (`MLModel != nil` — see docs/ml.md), every
backtest run also scores that model's correction-probability predictions against what actually
happened afterward (`ml.ComputeCorrectionLabel`, the same label definition used in training),
reporting Accuracy/Precision/Recall/F1/ROC AUC (ROC AUC via the rank-sum/Mann-Whitney method) in
the response's `ml_evaluation` field. This is `nil`, not a fabricated struct, when no real model
is loaded — evaluating the deterministic RSI-derived stub's made-up "correction probability"
against reality would be a meaningless number, not a real one.

**Persistence**: every `/backtests` call is now also written to `backtest_runs`
(migrations/005), append-only like recommendations/predictions, and `GET /backtests/history`
lists a symbol's past runs. Unlike the five-table target design below (backtests,
backtest_runs, trades, performance_metrics, strategy_versions), this collapses trades and
performance metrics into a single JSONB `result_summary` column rather than normalizing them
into their own tables — a deliberate simplification, not a partial implementation of all five;
see migrations/005's comment for the reasoning. `strategy_versions` isn't a real versioning
concept either, just a caller-supplied tag string per run.

## Overview
The Backtesting Engine is responsible for evaluating investment strategies using historical market data.
The purpose of backtesting is to verify whether Atlas recommendations would have produced acceptable results in historical market conditions.
Backtesting is mandatory before introducing any new strategy, indicator combination or ML model into production.
---
# Goals
The Backtesting Engine must:
- simulate historical decisions,
- evaluate strategy performance,
- compare different approaches,
- measure risk,
- prevent overfitting,
- validate ML predictions.
---
# Core Principles
## No Future Data Leakage
The engine must never use information that was not available at the decision timestamp.
Example:
Incorrect:
```
Use today's closing price to decide yesterday's action
```
Correct:
```
At timestamp T:
Use only data available before T
```
---
# Supported Actions
The engine must simulate:
```
BUY
SELL
HOLD
REDUCE POSITION
```
Each action must include:
- timestamp
- price
- quantity
- reason
- confidence
- model version
---
# Input Data
Historical data:
Prices:
- Open
- High
- Low
- Close
- Volume
Indicators:
- RSI
- EMA
- MACD
- ATR
- ADX
- Bollinger Bands
ML:
- prediction probability
- confidence
- model version
Portfolio:
- initial capital
- position size
- transaction fees
- currency
---
# Strategy Interface
Every strategy must implement:
```
Strategy
Evaluate()
GenerateSignal()
CalculateRisk()
```
Example:
```
RSI Strategy
ML Strategy
Trend Following Strategy
Correction Recovery Strategy
```
---
# Portfolio Simulation
The engine must simulate:
Initial capital:
```
10000 USD
```
Example:
BUY:
```
VOO
Price:
500 USD
Quantity:
10
```
Portfolio:
```
Cash
Positions
Profit/Loss
Drawdown
```

## Trading costs

Every simulated trade is charged `tradeCostRate` (0.1%) on each side, so a round trip costs 0.2%.
The entry fee comes out of the same cash that buys the shares, so a position is smaller than a
free trade would have bought, and the cost basis it carries into the decision engine
(`simPortfolio.AverageCost`) is the price actually paid. A round trip that gained less than the
fee counts as a losing trade, not a winning one.

Before this existed, every simulation traded for free. That flatters exactly the strategies a
backtest is most likely to be run on — the ones that trade often for a small edge each time — and
0.1% per side is the difference between a real result and an unreachable one for any of them. The
rate is a conservative retail default covering commission and spread; set it to the real brokerage
fee before reading a result as money.

---
# Performance Metrics
Every backtest must calculate:
## Returns
```
Total Return %
Annual Return %
```
---
## Risk
```
Maximum Drawdown
Volatility
Sharpe Ratio
Sortino Ratio
```
---
## Trading Metrics
```
Number of Trades
Winning Trades
Losing Trades
Win Rate
Average Gain
Average Loss
```
---
# ML Evaluation
The ML model must be evaluated independently.
Metrics:
```
Accuracy
Precision
Recall
F1 Score
ROC AUC
```
Additionally:
```
Prediction vs Reality
```
Example:
Prediction:
```
80% probability of correction
```
Reality:
```
Correction happened
```
---
# Walk Forward Testing
The engine must support walk-forward validation.
Example:
```
Train:
2015-2022
Validate:
2023
Test:
2024
```
Never randomly shuffle financial time series.

**Implementation status**: real, via `GET /backtests/walkforward`
(`backtesting.UseCase.RunWalkForward`, internal/application/backtesting/walkforward.go) — a
deliberate simplification of the target design above, scoped with the Author first: the split is a
fixed 60/20/20 proportion of whatever bar window is requested (`splitWalkForward`), not
caller-supplied explicit date ranges like the 2015-2022/2023/2024 example. Each split is a
strictly chronological, non-overlapping slice (never shuffled), and each gets its own
independent simulation — its own fresh indicator warm-up and simulated portfolio, exactly like a
standalone `Run` — so no split ever sees another's data, in either direction.
---
# Data Splitting
Allowed:
```
Training
Validation
Testing
```
Forbidden:
```
Random train/test split
```
because it creates unrealistic results.

**Implementation status**: real — see "Walk Forward Testing" above. `splitWalkForward` slices
chronologically only; there is no shuffling anywhere in this codebase's backtesting path.
---
# Optimization
The engine should support:
Parameter optimization:
Example:
```
RSI threshold:
20
25
30
35
```
Compare results:
```
Return
Risk
Drawdown
```

**Implementation status**: real, but scoped narrower than the literal RSI-threshold example
above (Atlas's engine blends technical/ML/portfolio into one score rather than trading on RSI
alone, so RSI in isolation isn't a real tunable parameter here). What's actually optimized is
`decision.Thresholds` — the five score-band cutoffs (StrongBullish/LeanBullish/LeanBearish/
StrongBearish/LowMLConfidence) that decide BUY/ACCUMULATE/HOLD/REDUCE/SELL/WAIT (see
docs/decision_engine.md and internal/domain/decision/types.go). `optimizeThresholds`
(internal/application/backtesting/walkforward.go) runs a small, fixed grid search around
`decision.DefaultThresholds()` (±0.05 for the four score bands, ±0.10 for LowMLConfidence — not
user-configurable), evaluated only against the train split, ranked by Sharpe Ratio specifically
so the search doesn't reward high-variance blowups (a single risk-adjusted number standing in
for comparing "Return/Risk/Drawdown" together, as this section asks). **Crucially: this never
touches the live decision engine.** The winning thresholds are reported back
(`WalkForwardResult.BestThresholds`) for a human to review — promoting them to
`decision.DefaultThresholds()` is a manual code change, never automatic. This was an explicit
scoping decision with the Author, consistent with PROJECT_CONTEXT.md's "Atlas is NOT ... an automatic
investment system."
---
# Reports
Generate:
HTML report
JSON report
Database record
Example:
```
Strategy:
ML Correction Detector
Period:
2015-2025
Return:
+125%
Max Drawdown:
-18%
Sharpe:
1.42
```
---
# Database Tables
Required:
```
backtests
backtest_runs
trades
performance_metrics
strategy_versions
```
---
# Future Extensions
Possible additions:
- Monte Carlo simulation
- Portfolio optimization
- Risk parity
- Factor analysis
- Reinforcement learning
- Paper trading integration

## ML Contribution — does the model actually make money?

`StrategyResult.MLContribution` (application/backtesting.measureMLContribution) runs theidentical simulation twice over the same bars — once with the ML term in the decision blend, once
with it removed — and reports the difference in return, Sharpe and max drawdown. Nil when only
the deterministic stub is loaded, same guard as `MLEvaluation`.

This closes a real gap. `MLEvaluation` reports ROC AUC, precision and recall; none of them say
whether *acting* on the model beats ignoring it. A model can rank well and still lose money, or
score modestly and still help. Judging a trading signal by classification metrics alone is the
standard mistake, and this project was making it: the backtester already computed Sharpe, the ML
evaluation already computed AUC, and nothing ever compared them.

Across all 47 collected symbols, 180-day window, 1215 simulated trades (25.9 per symbol):

| | better with ML | worse | no difference | mean delta | median delta |
|---|---|---|---|---|---|
| Total return | 23 | 24 | 0 | +0.008 | −0.000 |
| Sharpe ratio | 23 | 24 | 0 | +0.053 | −0.000 |
| Max drawdown | 30 | 16 | 1 | −0.025 | −0.014 |

(Drawdown delta is with-minus-without, so negative is the model helping.) In aggregate: mean
return +0.202 with ML against +0.194 without, mean max drawdown 0.217 against 0.243, win rate
0.343 against 0.334.

Read plainly: the ML term earns a modest place. Return and Sharpe are close to a coin flip
symbol by symbol, with medians at zero — for the typical symbol the model changes almost nothing.
Where it consistently helps is drawdown, improving it for 30 symbols against 16, which is exactly
the axis a correction model should move. It buys a little less depth in the bad stretches without
costing return.

**This replaced an earlier, much more damning reading** of the same measurement: mean Sharpe delta
−0.097, drawdown worse for 11 symbols and better for *none*. That reading was an artifact of two
bugs since fixed, and is worth recording because both were invisible in every statistical metric
the project computed:

1. The momentum term was frozen (see docs/decision_engine.md), so scores clustered and rarely
   crossed a threshold. The simulation averaged **1.0 trades per symbol** — each "delta" was the
   timing of a single entry. It is 25.9 now, from the same 180 days of history. The sample was
   never a data-availability problem; it was a symptom.
2. Yahoo emits a timestamp for exchange holidays and half-days with null OHLC, which unmarshalled
   to zero (see `providers.hasUsablePrices`). Final equity is `cash + quantity*close`, so a
   zero-priced last bar reported exactly −100% return and 100% drawdown. Four WSE large caps
   showed a total wipeout on a long-only strategy — Easter Monday, not a trading result — which
   alone dragged the mean return delta to −0.102 and made the model look like it was destroying
   Polish positions. WSE now reads +0.025 mean return delta, in line with US at +0.003.

The lesson is the one this struct exists for, sharpened: an economic measurement is only as
trustworthy as the bars underneath it, and a number that looks like a finding (−0.097 Sharpe,
drawdown worse for 11 and better for 0) can be a data bug wearing a conclusion's clothes.

**Remaining caveats.** The simulated strategy is all-in/all-out, and this is still one window of
one dataset. 1215 trades is enough to see a moderate effect and not a subtle one. Yahoo retains
roughly 730 days of hourly history against the 180 used here, so the sample can be deepened
about fourfold without waiting for time to pass.

