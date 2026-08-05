# Machine Learning Architecture

ML is a dedicated part of Atlas architecture.
Programming languages responsibility:

## Go

Go is the primary production language.

Go is responsible for:

- API
- market data collection
- technical analysis
- portfolio management
- decision engine
- notifications
- metrics
- orchestration


## Python

Python is used only for Machine Learning workflows.
Python responsibilities:
- dataset preparation
- feature engineering experiments
- model training
- model validation
- hyperparameter optimization
- model evaluation


Initial ML model:
XGBoost
The ML component must expose a stable interface.

Possible communication:
- REST API
- gRPC
- exported model format


The rest of the system must not depend directly on Python implementation details.
ML can be replaced without changing business logic.