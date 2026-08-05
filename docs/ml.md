# ML Architecture

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