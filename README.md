# Atlas - AI Portfolio Advisor

Atlas is an AI-powered portfolio decision support platform for stocks and ETFs.

The goal of the project is not to automatically trade assets but to assist investors by analyzing market data, technical indicators, machine learning predictions and portfolio state.

See [implementation-plan.md](implementation-plan.md) for the phased build plan and
[PROJECT_CONTEXT.md](PROJECT_CONTEXT.md) for an honest, up-to-date breakdown of what's actually
implemented versus still aspirational per module.

## Core Features

- Stock & ETF monitoring
- Technical analysis
- Machine Learning prediction
- Portfolio management
- Buy/Sell/Hold suggestions
- Discord notifications
- Prometheus metrics
- Grafana dashboards
- REST API
- TimescaleDB historical storage

## Tech Stack
Backend:
- Go

ML Training (offline, not part of the deployed service — see [training/README.md](training/README.md)):
- Python, XGBoost, exported to ONNX for Go inference

Database:
- TimescaleDB

Cache:
- Redis

Monitoring:
- Prometheus
- Grafana

Notifications:
- Discord Webhook

Deployment:
- Docker (Debian-based; bundles libonnxruntime for ML inference — see [Dockerfile](Dockerfile))
- Kubernetes Ready

## Project Philosophy
Architecture First

Performance First

Everything Observable

Everything Testable

Everything Configurable

No Trading Automation

Decision Support Only
