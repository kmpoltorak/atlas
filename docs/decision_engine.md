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
portfolio (`simPortfolio`), not the live one, for the same reason HasPosition does. Discord notifications
(internal/app/alerts_loop.go) are now gated to BUY/SELL/REDUCE only
(isHighPriorityAction), matching "Notifications" below — GET /alerts itself is unaffected and
still surfaces every non-HOLD action, including ACCUMULATE/WAIT; only the push notification is
restricted.

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

**Implementation status**: real, weighted 0.50/0.25/0.15/0.10 respectively (see this doc's top
status note and `internal/application/decision/orchestrator.go`'s `technicalWeight`/`mlWeight`/
`portfolioWeight`/`riskWeight` constants) — a documented first-pass heuristic, not derived from
data, same honesty stance as every other threshold in this engine.
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
SELL
REDUCE
```
should trigger Discord notifications.
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