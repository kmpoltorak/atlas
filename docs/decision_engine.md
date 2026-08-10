# Decision Engine

**Implementation status**: this document describes the target design. The actual engine
(internal/application/decision) now blends Technical (0.50) + ML (0.25) + Portfolio (0.15) +
Risk (0.10) into the overall score (Orchestrator.blendMLAndPortfolio, via NewOrchestratorWithML)
— matching the "Scoring Model" below's four-term formula, though the weights/heuristics are
still placeholders pending real tuning. ML itself is now a real trained model (XGBoost
correction-probability classifier, via ONNX — see docs/ml.md) when one is loaded (`MODEL_PATH`
set), falling back to the same deterministic RSI-derived stub as before when it isn't. There is
still no separate Risk Engine *stage* in the pipeline sense (Market Data → ... → Risk Engine →
Decision Engine, per "Decision Flow" below) — Risk is computed inline within
Orchestrator.blendMLAndPortfolio (`computeRiskScore`, from `portfolio.UseCase.ComputeRisk`'s
exposure/drawdown/volatility), not a separate pipeline component, but the score itself is real
and blended, closing that specific gap. The engine now emits all six action types below
(BUY/SELL/HOLD plus ACCUMULATE/REDUCE/WAIT — see decision.UseCase.BuildRecommendation's
threshold bands), gated on real portfolio position state (BUY vs. ACCUMULATE, SELL vs. REDUCE)
and ML confidence (HOLD vs. WAIT) — see this doc's "Recommendation Types" below and
PROJECT_CONTEXT.md for the exact rules and thresholds. Those five thresholds are a
`decision.Thresholds` value (internal/domain/decision/types.go), not hardcoded constants —
`decision.UseCase.Thresholds` defaults to `DefaultThresholds()` (the same cutoffs as before)
everywhere in live traffic, but backtesting's walk-forward optimizer (see docs/backtesting.md's
"Optimization" section) can construct a `UseCase` around different candidate thresholds to
evaluate against history without ever touching the live instance. Backtesting previously leaked
live portfolio state into simulated HasPosition — fixed now, see docs/backtesting.md's
Implementation status note. The real Recommendation object is `{Symbol, Action, Confidence,
Reason, Score, TechnicalScore, MLScore, PortfolioScore, RiskScore, MarketCondition, ModelVersion,
MLModelVersion}` — no separate timestamp field yet (the `created_at` column below covers it);
Reason does list which of ML/Portfolio/Risk meaningfully influenced the result. `ModelVersion`
and `MLModelVersion` are two different things, easy to conflate: `ModelVersion` is the decision
engine's own scoring-logic tag (`Orchestrator.EngineVersion`, a constant, "atlas-decision-v1"
today), while `MLModelVersion` is the real ML model that produced `MLScore` (`ml.Prediction.
ModelVersion` — a trained model's version, or `ml.StubModelVersion` when none is loaded). Both
are shown separately on the Recommendations dashboard page ("Engine" vs. "ML model"). Confidence
was previously a fixed literal per action band (every BUY reported 0.78, every HOLD reported 0.6,
regardless of how strong the actual score was — meaning most real recommendations, landing in the
"mixed signal" HOLD band, all reported an identical 60%) — fixed to scale continuously with how
far the blended score sits from the neutral center (0.5), the same idea `ml.UseCase.Predict`
already used for the ML sub-model's own confidence. WAIT is the one exception: it still reports
the real (low) ML confidence as-is, since that's the whole point of that action. RiskScore is
portfolio-wide (every held
position's exposure/drawdown/volatility, via `portfolio.UseCase.ComputeRisk`), distinct from
PortfolioScore (a single symbol's own P&L) — see "Risk Context" below. Every live recommendation
(GET /recommendations, GET /alerts, and the background alert-poll loop) is now persisted to the
`recommendations` table below (see internal/infrastructure/persistence/decision.go) as an
append-only history log — `risk_score` is now a real written value there too, not NULL.
Backtesting's individual simulated recommendations are still deliberately NOT persisted
one-by-one (see internal/application/backtesting/usecase.go) since they're hypothetical past
decisions, not real ones — but each full backtest run's summary now is (see
docs/backtesting.md), and its simulated RiskScore reflects the backtest's own simulated
portfolio (`simPortfolio`), not the live one, for the same reason HasPosition does. The ML term in the blend is no longer `1 - P(correction)`. It is now
`1 - P(correction) * (1 - P(recovery))` — one minus the probability of a drop that does *not*
recover (application/decision.mlScoreFromPrediction). Recovery probability had been computed,
persisted and displayed on the dashboard while every recommendation ignored it, even though it is
the better-validated of the two models. The risk worth pricing is not "will it dip" but "will it
dip and stay down"; a dip that recovers inside the recovery horizon is not a reason to avoid a
position. With no recovery model loaded RecoveryProbability is 0 and this degrades exactly to the
previous behaviour, so the recovery model stays optional here as everywhere else.

This term was previously averaged in alongside the technical score, which made a low correction
probability read as mildly bullish — a calm instrument scored well on it with no upside signal
behind it. It is now a multiplicative risk factor (see "Scoring Model" below), so it can only
lower a technical signal, never manufacture one.

Discord notifications
(internal/app/alerts_loop.go) are gated to the four actionable calls — BUY/ACCUMULATE/REDUCE/SELL
(isHighPriorityAction), matching "Notifications" below. GET /alerts itself is unaffected and
still surfaces every non-HOLD action, WAIT included; only the push notification is restricted.

## Overview
The Decision Engine is the core intelligence layer of Atlas.
Its responsibility is to combine:
- technical analysis,
- machine learning predictions,
- portfolio state,
- market conditions,
- risk management
and generate an investment suggestion.
The Decision Engine does not execute trades.
It only provides analytical recommendations.
---
# Objective
The system should answer:
```
Should I buy?
Should I add to my position?
Should I hold?
Should I reduce?
Should I exit?
```
---
# Decision Flow
```
Market Data
      |
Technical Analyzer
      |
ML Predictor
      |
Portfolio Analyzer
      |
Risk Engine
      |
Decision Engine
      |
Recommendation
```
---
# Inputs
## Market Analysis
Examples:
```
RSI
EMA trend
MACD
Volume
Volatility
Support levels
Resistance levels
```
---
## ML Prediction
Example:
```
Correction Probability:
82%
Recovery Probability:
75%
Confidence:
88%
```
---
## Portfolio Context
Example:
Existing position:
```
Ticker:
VOO
Quantity:
20
Buy price:
450 USD
Current price:
520 USD
Profit:
15%
```
---
## Risk Context
Examples:
```
Portfolio exposure
Position size
Volatility
Market trend
Drawdown
```

**Implementation status**: Exposure, Volatility, and Drawdown are real, honestly-computed numbers
(`portfolio.UseCase.ComputeRisk`, blended into scoring via `computeRiskScore` — see this doc's
top status note) — but "Volatility"/"Drawdown" here are cheap single-scan heuristics, not the
statistically real measures those names usually imply: Drawdown is unrealized-loss/exposure (no
tracked equity-curve peak), Volatility is the unweighted mean of \|CurrentPrice-AverageCost\|
across positions (not a return-based standard deviation) — see `portfolio.RiskSnapshot`'s doc
comment. The dashboard/portfolio pages label them "Unrealized Loss / Exposure" and "Avg.
Deviation from Cost" for this reason; only backtesting's genuinely statistical Volatility/
MaxDrawdown (see docs/backtesting.md) keep the "real" names. Position size is implicitly covered
by Exposure (sum across positions) rather than surfaced per-position here. Market trend is not
implemented — there's no trend/regime classifier (see PROJECT_CONTEXT.md's ML Engine section on
market condition).
---
# Recommendation Types
The engine returns:
```
BUY
ACCUMULATE
HOLD
REDUCE
SELL
WAIT
```
---
# Recommendation Object
Example:
```json
{
 "ticker":"VOO",
 "action":"REDUCE",
 "confidence":82,
 "score":76,
 "reason":[
   "RSI above 75",
   "ML predicts correction risk",
   "Position profit above target"
 ],
 "risk":"medium",
 "timestamp":"2026-08-05"
}
```
---
# Scoring Model
Final score:
```
Technical Score
+
ML Score
+
Portfolio Score
+
Risk Score
=
Final Decision Score
```

**Implementation status**: real, but not as a weighted sum. Technical is the only term that
measures an *opportunity*; the other three measure *risk*, and are applied as multiplicative
factors that can lower the technical score but never invent one:

```
score = technical × mlFactor × portfolioFactor × riskFactor
```

Each factor is 1 when its signal has nothing to penalize. Multiplication compounds deliberately:
a losing position, inside a portfolio drawdown, inside a correction warning is worse than any of
the three alone.

This replaced a four-term weighted average (0.50 / 0.25 / 0.15 / 0.10). The average was the wrong
shape, because averaging a risk signal with an opportunity signal makes *absence of risk* read as
*presence of opportunity*. Measured on the live watchlist on 2026-08-10, all three risk terms had
collapsed into a near-constant lift of roughly +0.15 on every symbol:

| term | live range | why it was inert |
|---|---|---|
| ML | 0.810 – 0.997 | estimates P(correction)/P(recovery) and has no upside model at all, so a calm instrument scored 0.97 with no bullish evidence |
| Portfolio | 0.500 – 0.500 | `portfolioRiskSignal` tops out at 0.5, so as an averaged term it dragged every score toward the middle even with nothing to say |
| Risk | 1.000 – 1.000 | `computeRiskScore` returns 1.0 for an empty portfolio — no drawdown and no volatility to measure, which the average read as maximum enthusiasm |

Only `technical` (0.295 – 0.878) was carrying information. That constant lift is why
`StrongBullish` moved from 0.75 to 0.60 in the same change: the threshold had to follow the score
whose meaning it depends on. The new value preserves the exact BUY set rather than retuning the
engine — measured against the live watchlist, the lowest current BUY lands at 0.6084 under the
new scoring and the highest current non-BUY at 0.5965, so nothing reorders.

Calibrating what that set *should* be is a separate question that needs outcome history, and is
deliberately still open.

## What the technical score does not do

Measured 2026-08-10 against 163,640 real hourly bars — 47 symbols, 720 days, the deepest history
Yahoo serves for this interval (729 days is the hard limit; 730 is rejected).

**No term in the technical blend has positive predictive power at any horizon tested.**
Information coefficient (Spearman rank correlation between the term and the realized forward
return, per symbol, then averaged):

| term | 3 bars | 6 bars | 12 bars | 24 bars |
|---|---|---|---|---|
| momentum/volume | −0.014 | −0.021 | −0.015 | −0.011 |
| RSI | −0.011 | −0.016 | −0.015 | −0.014 |
| Bollinger %B | −0.013 | −0.019 | −0.016 | −0.013 |
| OBV | −0.007 | −0.012 | −0.019 | −0.030 |
| blended | −0.015 | −0.021 | −0.017 | −0.014 |

Consistently and mildly *negative*, with only 13–21 of 47 symbols positive on any given cell.

**The BUY band captures almost nothing.** Mean 12-bar forward return by the band the blended score
lands in:

| band | bars | mean forward return | vs all bars |
|---|---|---|---|
| BUY ≥ 0.60 | 65,277 | 0.3457% | **+0.007pp** |
| 0.50–0.60 | 24,899 | 0.3104% | −0.029pp |
| 0.40–0.50 | 24,092 | 0.2752% | −0.064pp |
| weak < 0.40 | 45,988 | 0.3788% | +0.040pp |
| all bars | 160,256 | 0.3391% | — |

Entering at a BUY reading is worth 0.007pp over entering at a uniformly random moment, on a
baseline of 0.339%. Per symbol the BUY band beats that symbol's own baseline for 23 of 46 — a
coin flip.

**The contrarian reading does not rescue it.** The weak band looks better in the table, and it is
tempting to conclude the engine should be inverted. It should not:

- The edge is **+0.0397% per 12-bar hold**. A 0.05% round trip — conservative for a liquid US
  equity — leaves −0.010%. At 0.20%, realistic for a WSE mid cap, it leaves −0.160%. The edge is
  smaller than the cost of capturing it.
- It is not stable. Splitting the 720 days in half: the weak band's edge is +0.007pp in the first
  year and +0.072pp in the second, while the BUY band's runs −0.050pp then +0.064pp. Both flip.
- It is tail-heavy: SPCX +1.46%, CLSK +0.76%, RIOT +0.61% lead it — speculative names that
  crashed and rebounded — though 31 of 47 symbols do share the sign.

**Consequences for anyone reading this later.** Tuning the four sub-weights (0.45 / 0.20 / 0.20 /
0.15) is optimizing noise; the terms being weighted carry no measurable signal, so a "better"
weight set is a better fit to one sample. This is exactly why the measurement started with an
information coefficient rather than a grid search over backtest Sharpe: a grid search would have
returned confident-looking optimal weights and no hint that it was fitting nothing. Two
plausible-looking leads died the same way — OBV showed IC −0.096 and only 5 of 47 symbols
positive on a 180-day sample, which collapsed to −0.030 and 18 of 47 on the full history, and the
same 180-day sample made the BUY band look like the best performer, which the deeper sample
reversed.

None of this says the engine is broken or that the backtests lied. Backtests show gains because
the market rose over the window; what the score does not add is *selection* value. Atlas remains
useful as a surveillance and review tool over a watchlist, and the ML correction signal does earn
its place — it improves max drawdown on 30 symbols against 16 (see docs/backtesting.md). What the
technical score does not do is identify better-than-random moments to enter on hourly bars.

## The momentum/volume term

Technical Score is itself a weighted average (0.45 momentum/volume, 0.20 RSI, 0.20 Bollinger %B,
0.15 OBV). The heaviest of those, `momentumVolumeScore`, is:

```
momentum      = close_t / close_{t-12} - 1
normalized    = momentum / (realized_vol_20 * sqrt(12))
momentumScore = 0.5 + 0.5 * tanh(normalized / 1.5)
confirmation  = 0.5 + 0.5 * min(1, volume / mean_volume)
term          = 0.5 + (momentumScore - 0.5) * confirmation
```

Normalizing by the instrument's own realized volatility makes one threshold mean the same thing
for a liquid US megacap and a thinly traded WSE listing — the same scale-free stance the ML
feature vector settled on (docs/ml.md). Thin volume damps the reading toward neutral rather than
reversing it: a move nobody traded is weak evidence, not counter-evidence.

The window (12 bars), volatility window (20, shared with the ML feature vector), and tanh scale
(1.5) were picked by measuring candidates against the 41807 real closed hourly bars behind the
live watchlist, not by taste. At scale 1.0 the term saturates (p05 0.040, p95 0.971); at 2.0 it
compresses so a genuinely strong move never reads as strong (p95 0.853). At 1.5 it spends the
whole range: p05 0.139, p25 0.330, p50 0.509, p75 0.695, p95 0.880.

### What this replaced, and why it mattered

The term used to be a two-valued step function reading a **single** bar: `momentum > 0.02 && raw
volume/1000 > 1` returned 0.82, everything else 0.48. Three defects compounded, all measured
against production rather than argued:

1. It read the newest bar, which Yahoo serves as an **unclosed live quote** — `open == close`,
   `volume == 0`, timestamped at the moment of the request rather than a candle boundary. On
   2026-08-10 all 47 watchlist symbols carried exactly such a row as their newest bar. Momentum
   was therefore identically 0, so the bullish branch **could never fire on live traffic** and
   the largest term in the whole score sat frozen at 0.48.
2. Every BUY recorded in the week to 2026-08-10 (44 of them) landed in `0.750–0.754` against the
   0.75 threshold — grazing the line, never clearing it — and flipped back to HOLD on the next
   poll. SELL and REDUCE were unreachable entirely: not one was emitted. The engine was
   effectively a three-action engine reporting six.
3. Even on correct closed bars the rule fires on only 3.21% of them, so it contributed a
   near-constant with occasional 0.34-wide jumps. `volume/1000` also hard-codes the share-volume
   scale of a liquid US equity; that gate passes on 92% of bars, so it barely gated anything.
   `ComputeVolume` already produced the scale-free measure and was being ignored.

The replacement moves 0.053 per bar at the median (0.012 in the blended score), so the score
tracks price instead of quantizing it.

The unclosed bar is dropped at the source now — see `providers.dropUnclosedBars`. That fix also
matters to the ML path independently: `BuildFeatureVector` reads the newest bar's volume, so
inference always saw `volume = 0` while the model had been trained on real volumes. Acting only
on closed candles is the ordinary discipline for bar-based signals — anything computed on a
forming candle repaints until it closes.
---
# Example Calculation
## Technical
```
RSI:
20/25
Trend:
15/20
Volume:
10/15
```
Score:
```
45/60
```
---
## ML
```
Correction probability:
80%
Confidence:
90%
```
Score:
```
25/30
```
---
## Portfolio
```
Already profitable:
10/10
```
---
Final:
```
80/100
```
Recommendation:
```
REDUCE POSITION
```
---
# Explainability
Every recommendation must explain:
Why?
Which signals contributed?
Which indicators affected the result?
What is the uncertainty?
Never output:
```
BUY
```
without explanation.
---
# Rules
The Decision Engine must:
- be deterministic,
- be testable,
- store decisions,
- store input data,
- store model version,
- provide explanation.
---
# Database
Table:
```
recommendations
```
Fields:
```
id
ticker
action
score
confidence
technical_score
ml_score
portfolio_score
risk_score
explanation
created_at
model_version
```
---
# Notifications
High priority recommendations:
```
BUY
ACCUMULATE
REDUCE
SELL
```
should trigger Discord notifications.

These are exactly the actions that ask the reader to do something. HOLD and WAIT are excluded:
neither calls for a decision, and HOLD in particular is the steady state for most symbols most of
the time, so pushing it would bury the signals that matter.

ACCUMULATE was originally left out of this list while REDUCE was kept in, which made it
asymmetric — a partial sell notified, a partial buy stayed silent. There was no principle behind
the split, and ACCUMULATE is arguably the more actionable of the two, since by definition it
concerns a position already held.

Note also that notifications fire on *change*: the background loop re-evaluates the watchlist
every ALERT_POLL_INTERVAL_SECONDS (default 300), but only pushes when a symbol's action differs
from the previous poll. A symbol sitting in BUY for three days produces one notification, not
hundreds.

That change-detection map is seeded at startup from the recommendations log (`seedLastActions`
via `CurrentActionStreaks`), so a restart does not re-announce symbols that are merely continuing
to say what they already said. It previously lived only in memory, which was invisible while
actionable signals were rare and obvious the moment they weren't: four deploys on 2026-08-10
pushed the same seventeen BUY tickers four times over, none of which had changed action — ORCL
had read BUY continuously since 15:22.

The seed answers "what did the engine last say about this symbol", not "what did we last
successfully deliver". The difference is real and accepted: a Discord message that failed to send
before a restart is not retried after one. Tracking delivery properly would need its own table,
which a single-user tool doesn't warrant for a failure that is already logged as an error when it
happens. Within one process lifetime a failed send *is* retried — the action is recorded only
after the webhook accepts it, where the loop used to record it before even trying.

Each notification is a Discord embed (`buildAlertEmbed`), not a line of text: colour-coded by
action so an add reads differently from a trim at a glance, titled with the symbol and linked to
its quote page (`market.QuoteURL`, the same link the dashboard tables use), with
confidence/score/market condition as an inline field row and the rationale as one bullet per line.
The flat `[Atlas] ORCL: BUY (confidence 67%) — reason; reason; reason` string it replaced was
legible on its own and unscannable the moment a poll fired several at once.
Example:
```
🚨 Atlas Recommendation
Ticker:
VOO
Action:
REDUCE
Confidence:
82%
Reason:
- High RSI
- ML correction probability 85%
- Large unrealized profit
```
---
# Future Extensions
Possible improvements:
- LLM explanation layer
- sentiment analysis
- macroeconomic factors
- news analysis
- analyst ratings
- portfolio optimization
- automatic strategy discovery