#!/usr/bin/env python3
"""Trains an XGBoost classifier (correction-probability by default, or recovery-probability with
--target recovery) on the CSV produced by cmd/export-training-data and exports it to ONNX for Go
inference.

Feature order (rsi, ema, macd, atr, volume) matches internal/domain/ml.FeatureVector's field
order exactly and MUST stay in sync with internal/application/ml's ONNX-loading code — the
model has no column names at inference time, only positional floats.

This is a first, honest pass: a simple time-ordered holdout (not full walk-forward validation,
which is a separate, larger backtesting gap — see docs/backtesting.md), on whatever history
Atlas has actually collected (see PROJECT_CONTEXT.md's Implementation status notes), which may
be a small dataset. Class balance and holdout metrics are always printed and written to the
model's metadata sidecar so a degenerate/overconfident model is visible, not hidden.
"""
import argparse
import json
import sys
import time

import pandas as pd
from onnxmltools.convert import convert_xgboost
from onnxmltools.convert.common.data_types import FloatTensorType
from sklearn.metrics import accuracy_score, roc_auc_score
from xgboost import XGBClassifier

FEATURE_ORDER = ["rsi", "ema", "macd", "atr", "volume"]
HOLDOUT_FRACTION = 0.2


def time_ordered_split(df: pd.DataFrame, holdout_fraction: float) -> tuple[pd.DataFrame, pd.DataFrame]:
    """Splits each symbol's rows by time (last `holdout_fraction` -> holdout), not randomly,
    so no holdout row's feature window overlaps a training row's forward-looking label window.
    A random shuffle split would leak future information across the train/holdout boundary.
    """
    train_parts, holdout_parts = [], []
    for _, group in df.sort_values("ts").groupby("symbol"):
        cut = max(1, int(len(group) * (1 - holdout_fraction)))
        train_parts.append(group.iloc[:cut])
        holdout_parts.append(group.iloc[cut:])
    return pd.concat(train_parts), pd.concat(holdout_parts)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", default="training/data/features.csv")
    parser.add_argument("--output-dir", default="training/models")
    parser.add_argument("--version", default=None, help="defaults to a timestamp-based version")
    parser.add_argument("--target", choices=["correction", "recovery"], default="correction",
                         help="which label this CSV contains — only affects the output model's "
                              "filename prefix and metadata's label_definition text, the "
                              "training procedure itself is identical either way")
    args = parser.parse_args()

    model_prefix = "atlas-ml" if args.target == "correction" else "atlas-ml-recovery"
    label_definition = (
        "close drops >threshold_pct below the feature bar's close within horizon_bars forward "
        "bars (see internal/application/ml.CorrectionLabelParams)"
        if args.target == "correction" else
        "given a correction occurred at the feature bar (see CorrectionLabelParams), whether "
        "close recovered back to the pre-drop price within recovery_horizon_bars bars after the "
        "correction threshold was breached (see internal/application/ml.RecoveryLabelParams)"
    )

    df = pd.read_csv(args.input)
    missing = [c for c in FEATURE_ORDER + ["label"] if c not in df.columns]
    if missing:
        print(f"input CSV is missing required columns: {missing}", file=sys.stderr)
        return 1

    print(f"loaded {len(df)} rows across {df['symbol'].nunique()} symbols")
    class_counts = df["label"].value_counts().to_dict()
    print(f"label distribution: {class_counts}")
    if len(class_counts) < 2:
        print("only one label class present in the data; cannot train a classifier on this "
              "yet — collect more history or widen the correction threshold/horizon", file=sys.stderr)
        return 1

    train_df, holdout_df = time_ordered_split(df, HOLDOUT_FRACTION)
    if holdout_df["label"].nunique() < 2:
        print("holdout split ended up with only one label class; the dataset is too small for "
              "a meaningful per-symbol time-ordered split yet", file=sys.stderr)
        return 1

    # .values (not the DataFrame directly): onnxmltools' XGBoost converter expects the
    # booster's internal feature names to follow XGBoost's default "f0", "f1", ... pattern,
    # which only happens when XGBoost is trained on a plain array — a named DataFrame makes
    # XGBoost store the real column names internally, which the converter can't map back to
    # ONNX input positions ("Unable to interpret 'macd', feature names should follow 'f%d'").
    X_train, y_train = train_df[FEATURE_ORDER].values, train_df["label"]
    X_holdout, y_holdout = holdout_df[FEATURE_ORDER].values, holdout_df["label"]

    model = XGBClassifier(
        n_estimators=300, max_depth=4, learning_rate=0.05,
        eval_metric="auc", random_state=42,
    )
    model.fit(X_train, y_train)

    holdout_proba = model.predict_proba(X_holdout)[:, 1]
    holdout_pred = (holdout_proba >= 0.5).astype(int)
    auc = roc_auc_score(y_holdout, holdout_proba)
    accuracy = accuracy_score(y_holdout, holdout_pred)
    print(f"holdout AUC: {auc:.4f}, holdout accuracy: {accuracy:.4f} (n={len(holdout_df)})")

    onnx_model = convert_xgboost(
        model,
        initial_types=[("features", FloatTensorType([None, len(FEATURE_ORDER)]))],
    )

    version = args.version or ("v" + time.strftime("%Y%m%d%H%M%S"))
    onnx_path = f"{args.output_dir}/{model_prefix}-{version}.onnx"
    metadata_path = f"{args.output_dir}/{model_prefix}-{version}.json"

    with open(onnx_path, "wb") as f:
        f.write(onnx_model.SerializeToString())

    metadata = {
        "version": version,
        "trained_at": time.time(),
        "feature_order": FEATURE_ORDER,
        "row_count": len(df),
        "train_row_count": len(train_df),
        "holdout_row_count": len(holdout_df),
        "label_distribution": {str(k): int(v) for k, v in class_counts.items()},
        "holdout_auc": auc,
        "holdout_accuracy": accuracy,
        "label_definition": label_definition,
    }
    with open(metadata_path, "w") as f:
        json.dump(metadata, f, indent=2)

    print(f"wrote {onnx_path}")
    print(f"wrote {metadata_path}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
