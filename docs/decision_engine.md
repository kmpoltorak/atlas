# Decision Engine

**Implementation status**: this document describes the target design. The actual engine
(internal/application/decision) now blends Technical (0.55) + ML (0.30) + Portfolio-risk
(0.15) into the overall score (Orchestrator.blendMLAndPortfolio, via NewOrchestratorWithML) —
closer to the "Scoring Model" below than before, though the weights/heuristics are placeholders
and ML itself is still a deterministic stub (see docs/ml.md). There is still no separate Risk
Engine stage, and the engine only ever emits BUY/SELL/HOLD (not ACCUMULATE/REDUCE/WAIT). The
real Recommendation object is `{Symbol, Action, Confidence, Reason}` — no score, risk,
timestamp, or per-component (technical/ml/portfolio/risk) breakdown; Reason does list which of
ML/portfolio meaningfully influenced the result. Nothing is persisted to the `recommendations`
table described below — it exists in migrations/001 but nothing writes to it. Discord
notifications exist (internal/app/alerts_loop.go) but aren't gated by action type the way
"Notifications" below describes.

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