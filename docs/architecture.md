# Architecture

**Implementation status** (as of this pass — see PROJECT_CONTEXT.md for the up-to-date
per-module breakdown): Collector, Analyzer (RSI/EMA/MACD/ATR/Volume/OBV/ADX/Bollinger Bands —
the last three not yet blended into Decision Engine scoring), Decision Engine (technical + ML +
portfolio-risk blend), ML Engine (real XGBoost→ONNX training pipeline and Go inference, falling
back to a deterministic stub when no model is loaded — see docs/ml.md), Notification Service,
Metrics, REST API, and TimescaleDB storage for
prices/portfolio/watchlist/recommendations/predictions are real and wired end-to-end. Still
aspirational: ML Engine's recovery-probability/market-condition outputs (only correction
probability + confidence are real), Decision Engine's ACCUMULATE/REDUCE/WAIT actions (only
BUY/SELL/HOLD exist), a Risk Engine stage, and persistence for indicators (table exists,
nothing writes to it yet).

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

Implementation status: all of the above are implemented (internal/application/analyzer). OBV,
ADX, and Bollinger Bands are new — real, tested computations, but not yet consumed anywhere
(Decision Engine scoring and ml.FeatureVector still only use RSI/EMA/MACD/ATR/Volume). "detecting
trends" beyond what individual indicators already show (e.g. a consolidated trend/volatility
summary score) isn't implemented.


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
to ONNX, loaded via `github.com/yalue/onnxruntime_go` — see docs/ml.md and
training/README.md). Recovery probability and market condition are not implemented.


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