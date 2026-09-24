# Backtesting

`internal/application/backtesting` replays history through the live decision engine and reports
what following it would have done. It is a check on the engine, not a strategy library: there is
exactly one strategy, and nothing here ever changes live behaviour.

## What a run simulates

One all-in/all-out strategy driven by the Decision Engine: BUY deploys all cash, SELL liquidates.
ACCUMULATE and REDUCE are ignored, so a backtest cannot evaluate the graded part of the engine's
output. Simulating them needs a sizing policy — what fraction, on what basis, with what minimum
trade size — which is a product decision nobody has made, and inventing one would make the
reported returns a function of that invention rather than of the signal.

**Execution model — read this before reading any number below.**

- **Observation**: the decision at bar `i` sees `bars[:i]`, bars that have *closed*. It used to
  see bar `i`'s own close and volume, which are not known until that bar is over.
- **Execution**: at bar `i`'s **open**, the first price reachable after the signal. The earlier
  version filled at the same close the signal was computed from.
- **Quote freshness** is measured against the last bar, not the wall clock
  (`Orchestrator.HistoricalClock`), so a historical run is not flagged stale for being historical.
- **Costs**: `tradeCostRate` (0.1% commission per side) plus `slippageRate` (0.05% adverse fill
  per side), so a round trip costs about 0.3%. The entry fee comes out of the cash that buys the
  shares, the cost basis carried into the decision engine is the price actually paid, and a round
  trip that gained less than its costs counts as a loss. Both rates are placeholders for a liquid
  instrument, not measurements — set them to real fills before reading a result as money,
  especially on an illiquid WSE small cap. Before costs existed every simulation traded for free,
  which flatters exactly the strategies that trade often for a small edge.

**No live portfolio leaks in.** Each run builds its own `portfolio.UseCase` (`simPortfolio`, no
repository) and its own Orchestrator around it, synced to the simulated position before every
bar. BUY vs ACCUMULATE and SELL vs REDUCE, and the risk factor, follow the simulated position,
never what the live portfolio holds (`TestRunDoesNotLeakLivePortfolioState`). Indicator snapshots
are not written from backtests.

## What a run reports

`StrategyResult`: total and annualized return, Sharpe ratio, volatility, max drawdown, trade
count, winning and losing trades, win rate, and the trade list.

Volatility and Sharpe come from a daily equity curve (last observation per UTC date,
`dailyEquity`) scaled by the observations per year that curve actually contains
(`observationsPerYear`), not a hardcoded `sqrt(252)`. The old constant treated an hourly curve as
if each bar were a trading day, inflating the annualized figures by roughly `sqrt(24)`.

Every `/backtests` call is stored in `backtest_runs` (migrations/005) and listed by
`GET /backtests/history`. Trades and metrics live in one JSONB `result_summary` column rather
than separate tables; `strategy_version` is a caller-supplied tag, not a versioning system.

## ML evaluation

When a trained model is loaded, every run also scores its correction probabilities against what
actually happened (`UseCase.evaluateML`): accuracy, precision, recall, F1 and ROC AUC (rank-sum
method), in `ml_evaluation`. It is absent, not zero, when only the deterministic stub is loaded —
scoring the stub against reality would be a meaningless number.

The event it is scored against is the label the model was trained on, read from its metadata's
`label_params` (`ml.OnnxModel.LabelParams`) and reported as `MLEvaluation.Label`. A model whose
metadata predates `label_params` is scored against `DefaultCorrectionLabelParams()` with
`LabelFromModel=false`, shown on the Backtests page as "default label" — those numbers may
compare the model with an event it never learned.

## ML contribution — does the model make money?

`StrategyResult.MLContribution` (`measureMLContribution`) runs the identical simulation twice
over the same bars — once with the ML term in the decision, once without — and reports the
difference in return, Sharpe and max drawdown. Absent when only the stub is loaded.

This closes a real gap: ROC AUC, precision and recall do not say whether *acting* on the model
beats ignoring it. A model can rank well and still lose money, or score modestly and still help.

Across all 47 collected symbols, 180-day window, 1215 simulated trades (25.9 per symbol), with the
correction model of the time:

| | better with ML | worse | no difference | mean delta | median delta |
|---|---|---|---|---|---|
| Total return | 23 | 24 | 0 | +0.008 | −0.000 |
| Sharpe ratio | 23 | 24 | 0 | +0.053 | −0.000 |
| Max drawdown | 30 | 16 | 1 | −0.025 | −0.014 |

(Drawdown delta is with-minus-without, so negative is the model helping.) In aggregate: mean
return +0.202 with ML against +0.194 without, mean max drawdown 0.217 against 0.243, win rate
0.343 against 0.334.

Read plainly: return and Sharpe are a coin flip symbol by symbol, with medians at zero. Where the
model consistently helps is drawdown — 30 symbols against 16 — which is exactly the axis a
correction model should move.

**This replaced an earlier, much more damning reading** of the same measurement: mean Sharpe
delta −0.097, drawdown worse for 11 symbols and better for none. That was an artifact of two bugs,
both invisible in every statistical metric the project computed:

1. The momentum term was frozen ([decision_engine.md](decision_engine.md)), so scores rarely
   crossed a threshold and the simulation averaged **1.0 trades per symbol** — each "delta" was
   the timing of a single entry.
2. Yahoo emits holiday and half-day timestamps with null OHLC, which unmarshalled to zero
   (`providers.hasUsablePrices` now drops them). A zero-priced last bar reported −100% return;
   four WSE large caps showed a total wipeout on Easter Monday, which alone dragged the mean
   return delta to −0.102.

An economic measurement is only as trustworthy as the bars underneath it. This is still one
window of one dataset, and 1215 trades is enough to see a moderate effect, not a subtle one; Yahoo
keeps about 730 days of hourly history, so the sample can be deepened about fourfold.

## Walk-forward and threshold optimization

`GET /backtests/walkforward` (`UseCase.RunWalkForward`, `walkforward.go`) splits the requested
window 60/20/20 into train, validate and test — chronological, non-overlapping, never shuffled —
and runs each as an independent simulation with its own indicator warm-up and simulated
portfolio, so no split sees another's data.

`optimizeThresholds` then runs a small fixed grid around `decision.DefaultThresholds()` (±0.05 on
the four score bands, ±0.10 on `LowMLConfidence`) on the train split only, ranked by Sharpe so the
search does not reward high-variance blowups. The winner is reported
(`WalkForwardResult.BestThresholds`) for a person to review; it never touches the live engine,
and adopting it is a manual code change. Read [decision_engine.md](decision_engine.md)'s "What the
technical score does not do" before acting on one: a grid search over a signal with no measured
edge returns confident-looking thresholds anyway.

## Not built

A pluggable strategy interface or further strategies, partial position sizing, Sortino ratio,
HTML reports, and separate trades/metrics tables. None is in progress.
