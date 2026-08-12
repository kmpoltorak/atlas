# Architecture

**Implementation status** (as of this pass — this is the up-to-date per-module breakdown;
PROJECT_CONTEXT.md keeps only a short summary pointing back here, to stop the two from drifting
out of sync with each other): Collector, Analyzer (RSI/EMA/MACD/ATR/Volume/OBV/ADX/Bollinger Bands),
Decision Engine (technical + ML + portfolio-risk + risk-engine blend, all six actions
BUY/ACCUMULATE/HOLD/REDUCE/SELL/WAIT real and differentiated — see docs/decision_engine.md), ML
Engine (real XGBoost→ONNX training pipeline and Go inference, falling back to a deterministic
stub when no model is loaded — see docs/ml.md), Notification Service, Metrics, REST API, and
TimescaleDB storage for prices/portfolio/watchlist/recommendations/predictions are real and
wired end-to-end. OBV and Bollinger Bands are now blended into Decision Engine scoring too —
`buildTechnicalScore` folds a Bollinger %B position signal and an OBV accumulation/distribution
signal into `TechnicalScore.Overall` as weighted-average terms (weights 0.45 momentum/volume,
0.20 RSI, 0.20 %B, 0.15 OBV — first-pass heuristics, not tuned against outcome data) whenever
there's enough history to compute them. The momentum/volume term is a volatility-normalized
12-bar return confirmed by relative volume, replacing a step function that read a single bar and
in practice never fired, since Yahoo's newest "bar" is an unclosed live quote with `open ==
close` and `volume == 0`; that row is now dropped at the provider, which also stops ML inference
from always seeing `volume = 0` — see docs/decision_engine.md's "The momentum/volume term".
`ml.FeatureVector` still draws on only those same five
indicators (RSI/EMA/MACD/ATR/Volume), though as scale-free ratios rather than raw price-unit
values since the price levels themselves were letting the model identify the symbol — see
docs/ml.md. Extending past those five is the leading open lead for the correction model, which
currently loses to a per-symbol base-rate baseline. ADX is consumed too,
but only by the deterministic market-condition classifier (ADX+EMA, see docs/ml.md), not blended
into scoring or the ML feature vector either.
Recovery probability has a real, tested label definition but no trained model (too little
historical data to train one honestly — see docs/ml.md); market condition is real and live
(`ml.ClassifyMarketCondition`, a deterministic rule, not a trained model). The Risk Engine is
real and blended into scoring (`computeRiskScore`, weight 0.10) but still computed inline in the
Orchestrator rather than as a separate pipeline stage. Indicators are now persisted from every
live-recommendation path — `GET /recommendations`, `GET /alerts`, and the background alert-poll
loop — but deliberately not from backtesting, whose bars are historical/simulated rather than
live readings (see implementation-plan.md's Phase 3 status note).

## Core Modules

### Collector

Responsible for collecting market data from external providers.

Responsibilities:

- downloading stock and ETF prices
- normalizing market data
- storing historical data


### Analyzer

Responsible for market analysis and technical indicators.

Responsibilities:

- calculating indicators
- detecting trends
- generating market features

Indicators:

- RSI
- EMA
- MACD
- ATR
- ADX
- OBV
- Bollinger Bands
- Volume analysis

Implementation status: all of the above are implemented (internal/application/analyzer).
RSI/EMA/MACD/ATR/Volume/OBV/Bollinger Bands are all blended into Decision Engine scoring
(`buildTechnicalScore`); ADX feeds market-condition classification instead (ADX+EMA — see
docs/ml.md); `ml.FeatureVector` draws on those same five, as scale-free ratios (see this file's
top status note). "detecting trends" beyond what individual indicators already show (e.g. a
consolidated trend/volatility summary score) isn't implemented.


### ML Engine

Responsible for machine learning predictions.

The ML Engine predicts:

- correction probability
- recovery probability
- market condition
- confidence score

The ML Engine is executed in the Go production runtime.

ML models are trained externally using Python.

Implementation status: correction probability and confidence score are real (XGBoost, exported
to ONNX, loaded via `github.com/yalue/onnxruntime_go` — see docs/ml.md and training/README.md).
Market condition is real too, but deliberately not ML — a deterministic ADX+EMA rule (see
docs/ml.md). Recovery probability has a real, tested label definition but no trained model yet
— real accumulated history is still too thin to train one honestly.


### Portfolio Engine

Responsible for user portfolio management.

Responsibilities:

- tracking owned positions
- calculating profit/loss
- calculating exposure
- portfolio risk analysis


### Decision Engine

The main business intelligence component.

Responsible for generating investment suggestions.

Combines:

- technical analysis
- ML predictions
- portfolio context
- risk analysis

Produces:

- BUY
- ACCUMULATE
- HOLD
- REDUCE
- SELL
- WAIT


### Notification Service

Responsible for notifications.

Initial integration:

- Discord Webhooks


### Metrics

Responsible for observability.

Technology:

- Prometheus exporter

Metrics include:

- system metrics
- market metrics
- ML metrics
- decision metrics


### REST API

Application interface.

Provides:

- market data
- portfolio information
- recommendations
- system status


### Storage

Primary database:

- TimescaleDB

Stores:

- historical prices
- indicators
- portfolio data
- predictions
- recommendations


### Cache

Technology:

- Redis

Used for:

- temporary calculations
- API caching
- rate limiting


---

# Machine Learning Architecture

Atlas separates ML model development from production execution.

## Training Layer

Language:

Python

Purpose:

Develop and validate machine learning models.

Responsibilities:

- dataset preparation
- feature engineering
- exploratory analysis
- model training
- model validation
- hyperparameter optimization
- model evaluation


Typical tools:

- pandas
- numpy
- scikit-learn
- XGBoost


Output:

Versioned ML model artifact.


---

## Production Inference Layer

Language:

Go

Purpose:

Execute ML predictions inside the production system.

Responsibilities:

- loading trained models
- generating predictions
- providing prediction scores
- integrating results into Decision Engine
- exposing ML metrics


The production system must not depend on Python runtime.


---

## ML Workflow
Historical Market Data

    |

    v

Python Training Pipeline

    |

    v

Validated Model Artifact

    |

    v

Go ML Runtime

    |

    v

Decision Engine

    |

    v

Investment Recommendation


---

## ML Principles

- Models must be versioned.
- Every model must have evaluation metrics.
- Training and production inference are separated.
- ML predictions are supporting signals, not final decisions.
- The Decision Engine is responsible for final recommendations.