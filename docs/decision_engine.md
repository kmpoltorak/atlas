# Decision Engine

The decision engine (`internal/application/decision`) turns one symbol's closed bars, the ML
models' output and the whole portfolio into a recommendation: BUY, ACCUMULATE, HOLD, REDUCE, SELL
or WAIT, with a confidence and the reasons behind it. It never executes anything.

## How a recommendation is built

`Orchestrator.BuildRecommendationFromBars`, for one symbol:

1. **Technical score** (`buildTechnicalScore`) — a weighted average of momentum/volume (0.45), RSI
   (0.20), Bollinger %B (0.20) and OBV (0.15), each on 0–1. This is the only term that measures an
   *opportunity*. See "The momentum/volume term" and "What the technical score does not do" below.
2. **ML factor** — from the correction and recovery models, when the ML term is switched on.
3. **Portfolio factor and risk factor** — from every position in every portfolio.
4. **Score** — the technical score times the three factors, which can lower it but never raise it.
5. **Action** (`UseCase.BuildRecommendation`) — the score's band, whether the symbol is held, and
   the ML model's confidence.
6. **Market condition** — BULL/BEAR/SIDEWAYS from a deterministic ADX+EMA rule
   (`ml.ClassifyMarketCondition`), reported alongside; it does not change the score.

Every live recommendation — `GET /recommendations`, `GET /alerts` and the background poll — is
appended to the `recommendations` table with every field below. Backtests do not write
individual recommendations ([backtesting.md](backtesting.md)).

## Scoring model

```
score = technical × mlFactor × portfolioFactor × riskFactor
```

Each factor is 1 when its signal has nothing to penalize. Multiplication compounds deliberately:
a losing position, inside a portfolio drawdown, inside a correction warning is worse than any of
the three alone.

- **ML factor.** `mlScoreFromPrediction` = `1 − P(correction) × (1 − P(recovery))`: one minus the
  probability of a drop that does *not* recover. The risk worth pricing is not "will it dip" but
  "will it dip and stay down". With no recovery model loaded P(recovery) is 0 and this reduces to
  `1 − P(correction)`. With no correction model loaded the deterministic stub answers
  ([ml.md](ml.md)).
- **Portfolio factor.** `portfolioRiskSignal` — the symbol's own position P&L, on a 0–0.5 scale
  where 0.5 means nothing to penalize.
- **Risk factor.** `computeRiskScore` over `portfolio.UseCase.ComputeRisk`'s portfolio-wide
  exposure, drawdown and volatility.

The Settings page's ML toggle (initially `ML_ENABLED`) removes only the ML factor: no prediction
is made and `ml_model_version` stays empty. The portfolio and risk factors still apply, since
they do not depend on the model.

**Why multiplication.** This replaced a four-term weighted average (0.50 technical / 0.25 ML /
0.15 portfolio / 0.10 risk). Averaging a risk signal with an opportunity signal makes *absence of
risk* read as *presence of opportunity*. Measured on the live watchlist on 2026-08-10, all three
risk terms had collapsed into a near-constant lift of roughly +0.15 on every symbol:

| term | live range | why it was inert |
|---|---|---|
| ML | 0.810 – 0.997 | estimates P(correction)/P(recovery) and has no upside model at all, so a calm instrument scored 0.97 with no bullish evidence |
| Portfolio | 0.500 – 0.500 | `portfolioRiskSignal` tops out at 0.5, so as an averaged term it dragged every score toward the middle even with nothing to say |
| Risk | 1.000 – 1.000 | `computeRiskScore` returns 1.0 for an empty portfolio — no drawdown and no volatility to measure, which the average read as maximum enthusiasm |

Only `technical` (0.295 – 0.878) was carrying information. That constant lift is why
`StrongBullish` moved from 0.75 to 0.60 in the same change: the threshold had to follow the score
whose meaning it depends on. The new value preserves the exact BUY set rather than retuning the
engine — measured against the live watchlist, the lowest current BUY lands at 0.6084 under the
new scoring and the highest current non-BUY at 0.5965, so nothing reorders. Calibrating what that
set *should* be needs outcome history, and is deliberately still open.

## Portfolio and risk context

Both are deliberately all-portfolio: `blendMLAndPortfolio` reads
`portfolio.UseCase.GetAllPositions` (every portfolio combined, quantity-weighted average cost per
symbol), not the dashboard's active tab. A symbol bought in one portfolio reads as ACCUMULATE, not
BUY, whichever portfolio is on screen. The Dashboard and Portfolio pages still show only the
active portfolio.

The portfolio-wide risk numbers are cheap single-scan heuristics, not the statistical measures
the names usually imply (`portfolio.RiskSnapshot`): drawdown is unrealized loss over exposure, with
no equity-curve peak, and volatility is the mean |current price − average cost| across positions,
not a standard deviation of returns. The dashboard labels them "Unrealized Loss / Exposure" and
"Avg. Deviation from Cost" for that reason; only backtesting's volatility and max drawdown are the
real statistics.

Every risk number is computed against each position's current price. A held position with no
current market quote — none yet, or one more than four days old (`portfolio.QuoteMaxAge`, so a
Friday quote still counts over a long weekend) — is valued at its last
trade, which makes its P&L exactly zero and would read as "nothing wrong". The recommendation
instead reports `risk_data_available: false`, `degraded: true` and a reason naming the symbols.
Freshness is measured against the market's trade time (`symbols.quoted_at`), not when Atlas
fetched the quote, since a provider keeps returning a stopped instrument's last price.

## Actions and thresholds

`decision.DefaultThresholds()` (`internal/domain/decision/types.go`):

| score | held | not held |
|---|---|---|
| ≥ 0.60 (`StrongBullish`) | ACCUMULATE | BUY |
| 0.55 – 0.60 (`LeanBullish`) | WAIT or HOLD | WAIT or HOLD |
| 0.45 – 0.55 | HOLD | HOLD |
| 0.25 – 0.45 (`LeanBearish`) | REDUCE | WAIT or HOLD |
| ≤ 0.25 (`StrongBearish`) | SELL | WAIT or HOLD |

"WAIT or HOLD": WAIT when the ML model's own confidence is below 0.30 (`LowMLConfidence`) — a
lean the model is not sure about — and HOLD otherwise. The walk-forward optimizer can evaluate
other thresholds against history but never changes these ([backtesting.md](backtesting.md)).

## Recommendation object

A real response, `GET /recommendations?symbol=VOO` on 2026-09-24 against the two model files then
active (a fresh database, so no portfolio and therefore no measured risk):

```json
{
    "symbol": "VOO",
    "action": "HOLD",
    "confidence": 0.4001435566082061,
    "reason": [
        "Mixed signal composition",
        "Decision-support layer only"
    ],
    "score": 0.29992822169589695,
    "technical_score": 0.30505926754249674,
    "ml_score": 0.9831801672903282,
    "portfolio_score": 0.5,
    "risk_score": 1,
    "risk_data_available": false,
    "market_condition": "BEAR",
    "model_version": "atlas-decision-v2",
    "ml_model_version": "v20260810145759",
    "ml_recovery_model_version": "v20260809134009"
}
```

- Every score is on a 0–1 scale.
- `confidence` is how far `score` sits from the neutral 0.5, scaled to 0–1 (`|score − 0.5| × 2`),
  so a confident SELL and a confident BUY both have high confidence. WAIT is the exception: it
  reports the ML model's own (low) confidence, since that is the reason for the WAIT. The ML
  confidence otherwise only decides WAIT versus HOLD.
- `degraded: true` appears when an expected input was missing: the ML prediction failed, the
  portfolio could not be read, or a held position has no current price. The reasons say which.
  The dashboard and alert embeds show it as "incomplete inputs".
- `model_version` is the engine's own scoring-rules tag (`Orchestrator.EngineVersion`), bumped
  whenever scoring or flagging rules change. `atlas-decision-v2` began with quote freshness
  measured against the market's trade time and recorded input quality; v1 rows span several
  earlier scoring revisions and do not identify one rule set. `ml_model_version` and
  `ml_recovery_model_version` name the models that produced `ml_score` (`stub-v0` when the stub
  answered). The Recommendations page shows them as "Engine" and "ML model".
- `degraded`, `risk_data_available` and `ml_recovery_model_version` are persisted from
  migrations/022; older rows hold NULL, since that history never recorded them.

The ML inputs behind it, from `GET /predictions?symbol=AAPL` on the same day with the same models:

```json
{"symbol":"AAPL","confidence":0.9344841241836548,"score":0.9672420620918274,
 "correction_probability":0.03275793790817261,"recovery_probability":0.425618052482605,
 "model_version":"v20260810145759","recovery_model_version":"v20260809134009"}
```

### Worked example

The VOO recommendation above (`Orchestrator.blendMLAndPortfolio`):

```
score = technical × ML factor × (portfolio / 0.5) × portfolio-wide risk
      = 0.30506   × 0.98318   × (0.5 / 0.5)       × 1
      = 0.29993
```

0.29993 falls between `StrongBearish` (0.25) and `LeanBearish` (0.45). Nothing is held, so there is
nothing to reduce; the ML confidence is well above 0.30, so the action is HOLD rather than WAIT.
`confidence` = |0.29993 − 0.5| × 2 = 0.40.

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

## The dip-entry rule, and why it was not shipped

Measured 2026-08-11 against 226,608 real hourly bars — 45 symbols, everything Yahoo served for
the interval, spanning 1,055 days. (FBTC and IBIT are absent: both are younger than the request
window and Yahoo rejects it with HTTP 422.)

The rule tested was the obvious one, and the right one to test — buy the dips:

```
ENTRY:  price >= 1.5x threshold below its 10-bar high
        threshold = max(4 * trailing 20-bar realised volatility, 2%)
EXIT:   60 bars later
```

The volatility scaling is what makes it a rule rather than a hunch: a 3% drop means something
different for ORCL than for a thin WSE listing, and dividing by the instrument's own volatility
makes one threshold mean the same thing everywhere.

**It has no edge.** Mean 60-bar forward return at a state entry, against the mean across all bars:

| variant | window | entries | edge |
|---|---|---|---|
| high of highs | 1,055 days | 2,880 | **−0.260%** |
| high of closes | 1,055 days | 1,538 | **+0.339%** |
| high of highs | last 720 days | 2,003 | +0.128% |
| high of closes | last 720 days | 1,070 | +0.738% |

Those four rows are the same rule on the same data. What separates them is whether the "10-bar
high" is taken over bar highs or bar closes — a bookkeeping detail nobody would defend on
economic grounds — and how far back the window reaches. A real edge does not change sign when you
pick the other equally arbitrary convention.

The stability checks agree, and they are the reason this is a verdict rather than a doubt. Across
consecutive thirds of each symbol's own history the edge runs −0.744% / −0.458% / +0.373% (highs)
and −0.400% / +0.514% / +0.867% (closes): the whole apparent edge lives in the most recent third
and is absent or negative before it. Per symbol it is 21 of 44 and 24 of 42 — a coin flip. At the
median, entries return +0.708% and +1.125% against a baseline median of +1.010%, so it straddles
zero there too.

**How this was nearly shipped.** An earlier pass measured the same rule over 720 days and recorded
+0.585% at 1.5×, with robustness checks that passed: positive for 34 of 46 symbols, median close
to mean, stable across both halves. Every one of those checks was computed inside the window that
happens to contain the effect. Deepening the sample by 335 older days removed it. That is the
third time on this project a signal has survived its own sample and died on a larger one (see the
OBV and BUY-band leads above) — the pattern is now reliable enough to state as a rule: **a result
measured on one window is a hypothesis, not a finding.**

The rule is not in the codebase. It was written, measured, and deleted; this section is what
remains of it, so the next person to have the idea — including a future session reading its own
notes — starts from the measurement instead of repeating it.

## Notifications

The background poll pushes BUY, ACCUMULATE, REDUCE and SELL (`isHighPriorityAction`) to every
configured channel — Discord, Slack, Telegram. These are exactly the actions that ask the reader to
do something. HOLD and WAIT are excluded: neither calls for a decision, and HOLD is the steady
state for most symbols most of the time, so pushing it would bury the signals that matter.
`GET /alerts` and the Alerts page still show every non-HOLD action, WAIT included.

ACCUMULATE was originally left out while REDUCE was kept in — a partial sell notified, a partial
buy stayed silent. There was no principle behind the split, and ACCUMULATE is arguably the more
actionable of the two, since by definition it concerns a position already held.

Notifications fire on *change*: the loop re-evaluates the watchlist every
`ALERT_POLL_INTERVAL_SECONDS` (default 300) but only pushes when a symbol's action differs from
the last one announced. A symbol sitting in BUY for three days produces one notification, not
hundreds.

That comparison reads a durable cursor, `alert_cursor`, at the start of every poll. Two earlier
designs failed here. The first kept the map purely in memory, which was invisible while
actionable signals were rare and obvious the moment they were not: four deploys on 2026-08-10
pushed the same seventeen BUY tickers four times over, none of which had changed action. The
second seeded that map at startup from the `recommendations` log, which answers "what did the
engine last *say*", not "what did we last *announce*" — and since ordinary HTTP browsing writes
that table too, a crash between recording a recommendation and queuing its alert left history
claiming BUY with nothing pending, and the restart suppressed the alert as unchanged.

`alert_cursor` records only what was actually announced, committed in the same transaction as the
`alert_outbox` row that announces it: neither can exist without the other. The outbox row is
written *before* anything sends, so a process that dies mid-send loses nothing, and a retry goes
only to the channels that still owe the alert. The guarantee is at-least-once per channel; see
[deployment.md](deployment.md)'s "Delivery guarantee".

Each Discord notification is an embed (`buildAlertEmbed`, `internal/app/alerts_loop.go`): titled
`<SYMBOL> — <ACTION>`, colour-coded by action and linked to the symbol's quote page
(`market.QuoteURL`, the same link the dashboard tables use); a field row with Confidence, Score,
Market (when classified) and Inputs "Incomplete — see reasons" (when degraded); the reasons as a
bullet list; and the footer "Atlas · decision support only, no trades are executed". The flat
one-line string it replaced was legible on its own and unscannable the moment a poll fired
several at once.
