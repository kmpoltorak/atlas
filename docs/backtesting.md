# Backtesting Engine

**Implementation status**: internal/application/backtesting implements a real walk-forward
simulation (no future *bar* data leakage, verified by regression test) of exactly one fixed
all-in/all-out strategy driven by the live Decision Engine — not the pluggable Strategy
interface described below, and no other example strategies exist. It computes Returns, Max
Drawdown, Volatility, Sharpe Ratio, and Win Rate. Not yet implemented: the train/validate/test
Walk Forward Testing date-range splitting described below (the "no lookahead" guarantee is
real for price bars; the separate train/validate/test period concept is not), ML Evaluation
metrics (Accuracy/Precision/Recall/F1/ROC AUC), parameter Optimization, and all of the Database
Tables listed below — nothing is persisted, results are returned as JSON only
(GET /backtests).

A real caveat, not yet fixed: because backtesting reuses the exact same Orchestrator instance as
live recommendations (see internal/app/app.go), every simulated recommendation's
`decision.RecommendationInput.HasPosition` (see docs/decision_engine.md's ACCUMULATE/REDUCE/WAIT
differentiation) reflects the **live, real, present-day** portfolio, not the backtest's own
simulated position built up bar-by-bar in the simulation loop. This can make a backtest's action
sequence (BUY vs. ACCUMULATE, SELL vs. REDUCE) depend on unrelated present-day portfolio state —
a form of leakage this doc's "No Future Data Leakage" principle doesn't cover, since it's not
about *bar* data. Fixing it properly needs the backtest loop to feed the Orchestrator its own
simulated position instead of querying the live Portfolio use case — not done here.

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