# ML Architecture

**Implementation status**: none of the Python training pipeline described below exists yet.
The Go production inference path (internal/application/ml) is a deterministic stub — it
returns a score derived from the RSI feature plus a fixed 0.72 confidence and a
"stub-v0" model version, not output from a trained model. `BuildFeatureVector` (real,
analyzer-backed) is the only genuinely "real" piece of this module today. Every live prediction
(GET /predictions, and indirectly every recommendation that blends ML in) is now persisted to
the `predictions` table (see internal/infrastructure/persistence/ml.go) as an append-only
history log — but only into a `score` column added specifically for this stub's output
(migrations/004); `correction_probability`, `expected_duration`, and `expected_strength` (the
columns matching this doc's target design below) stay NULL until a real model produces that
richer output.

## Training

Framework:
Python ecosystem
Possible tools:
- pandas
- numpy
- scikit-learn
- XGBoost
- Optuna

## Model Lifecycle
1. Collect historical data
2. Generate features
3. Train model
4. Validate model
5. Export model
6. Deploy model

## Production Inference
The production system is written in Go.
The ML model should be consumed through a stable interface.
The model implementation must be replaceable.