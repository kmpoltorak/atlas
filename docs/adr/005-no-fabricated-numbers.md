# 005 — Absent data is reported as absent, never substituted

**Status**: Accepted (2026-09-05)

## Context

The recurring failure mode across this codebase has not been crashes. It has been plausible
numbers with nothing behind them:

- A position with no market quote was priced at its last *trade*, so its unrealized P&L was zero
  by construction. The dashboard overlaid a live quote; the decision engine read the raw column.
  Buy at 100, watch it fall to 70, and the screen showed a loss while the engine scored the
  position as though it were still worth 100.
- `blendMLAndPortfolio` discarded errors from `Predict` and `GetAllPositions`, producing a
  recommendation shaped exactly like a healthy one but missing a whole input.
- `quoteToPLN` returned an unconvertible foreign-currency quote unchanged, which everything
  downstream then read as PLN.
- `computeRiskScore` returned 1.0 for an empty portfolio — maximum enthusiasm for an absence of
  information.
- Training reported a holdout score from a split whose boundary differed per symbol, so one
  symbol's training rows could postdate another's holdout.

None of these looked wrong. That is what makes them worse than a crash.

## Decision

Where a value is unavailable, say so in the type, and make the consumer handle it.

- `portfolio.Position` carries `PricedAt` and `PriceSource`. A fallback price is labelled as one,
  and `IsLive` is what decides whether it may be treated as a valuation.
- `decision.Recommendation.Degraded` marks a recommendation produced with a missing input, and
  `Reason` says which.
- `quoteToPLN` returns an error rather than an unconverted quote.
- The FX fallback carries an age and expires.
- A transaction dated in the past converts at that date's published rate, or fails — it does not
  silently use today's.
- Training records the actual boundary dates in the model's metadata, and the engine refuses to
  backtest a model over a period overlapping its own training window.

## Consequences

More errors reach the user, and some operations that used to "work" now refuse. That is the
intended trade: a refusal is recoverable, a wrong cost basis silently poisons every figure
derived from it.

The cost is that every new signal needs an explicit unavailable state designed alongside it,
rather than a zero value that happens to look neutral.

## What would make this wrong

Nothing. If a number cannot be computed honestly, it should not be shown at all.
