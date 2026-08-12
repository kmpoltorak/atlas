# Finance

- Atlas **never executes trades** — no brokerage integration exists or should be added without
  an explicit, separate design conversation. It is decision support only; this is a hard
  constraint, not a phrasing preference (see `PROMPT.md`).
- Six real actions, not five: `BUY`, `ACCUMULATE`, `HOLD`, `REDUCE`, `SELL`, `WAIT`
  (`internal/domain/decision`). `ACCUMULATE`/`REDUCE` vs `BUY`/`SELL` are gated on whether a real
  portfolio position already exists, not just score severity — see `docs/decision_engine.md`.
- The score is a real four-term blend — Technical (0.50) + ML (0.25) + Portfolio P&L (0.15) +
  Portfolio-wide Risk (0.10), `internal/application/decision`'s `Orchestrator`. These weights are
  documented first-pass heuristics (`docs/decision_engine.md` says so explicitly), not tuned or
  backtested-optimal — don't treat them as settled without checking that doc first.
- Every recommendation persists with a real `Reason []string` explanation, confidence score, and
  `RiskScore`/`MarketCondition` fields — never just an action label. `GET /recommendations`'s
  response and the `recommendations` table (migrations/001/007) both carry the full explanation,
  not just the verdict.
- Confidence and score are two different things: `Confidence` reflects how far the ML prediction
  sits from 0.5 (uncertain) toward 0/1 (certain); `Score` is the blended decision value that maps
  to an action band. Don't conflate "the model is confident" with "the recommendation is
  positive" — a confident SELL and a confident BUY both have high `Confidence`.
- Market condition (`BULL`/`BEAR`/`SIDEWAYS`) is a deterministic ADX+EMA technical rule
  (`ml.ClassifyMarketCondition`), not an ML prediction — don't describe it as "the model
  predicts the market is bullish," it's a rule, not a trained output.