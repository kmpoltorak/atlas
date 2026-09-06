# 004 — Backtests signal on closed bars and fill at the next open

**Status**: Accepted (2026-09-05)

## Context

The simulation showed the decision engine `bars[:i+1]` — including bar *i*'s own close and volume,
which are not known until that bar is over — and then bought or sold at that same close.

Both halves are the same error: the strategy needed the closing price to produce the signal, and
then transacted at the price it had just used to decide. No order can be placed at a price that
is an input to the decision to place it. Adding a commission did not touch this; it made a
physically impossible fill slightly more expensive.

Separately, `sqrt(252)` was applied to an equity curve sampled hourly, treating every bar as a
trading day and inflating annualized volatility and Sharpe by roughly `sqrt(24)`.

## Decision

Three distinct times, named separately:

- **Observation**: bar `i` decides on `bars[:i]` — bars that have closed.
- **Execution**: at bar `i`'s open, the first price reachable after the signal.
- **Cost**: `tradeCostRate` (commission) plus `slippageRate` (adverse fill), charged on both
  sides, so a round trip pays four times.

Annualization is computed from the curve's own dates (`observationsPerYear`) after reducing to one
observation per UTC trading day, not from a constant.

Commission and slippage are separate constants because they are separate things: one is a fee you
can look up, the other is what the market does to a marketable order. Both are placeholders for a
liquid instrument, and both are calibration knobs — the physical world needs tuning a minimal
model cannot see.

## Consequences

Reported returns dropped. That is the point: the previous numbers included an edge that came from
the fill, not the strategy, and the effect was largest for exactly the strategies a backtest most
flatters — the ones trading often for a small edge each time.

Position sizing remains all-in/all-out. `ACCUMULATE` and `REDUCE` are not simulated, so a backtest
cannot evaluate the graded part of the engine's output at all. That is stated in
`docs/backtesting.md` rather than papered over: implementing it needs a sizing policy nobody has
decided, and inventing one would make the returns a function of the invention.

## What would make this wrong

Nothing about the direction. The magnitudes are guesses until calibrated against real fills.
