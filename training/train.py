#!/usr/bin/env python3
"""Trains an XGBoost classifier (correction-probability by default, or recovery-probability with
--target recovery) on the CSV produced by cmd/export-training-data and exports it to ONNX for Go
inference.

Feature order (rsi, ema_dist, macd_pct, tr_pct, atr_pct, rvol_20, rvol_50, volume) matches internal/domain/ml.FeatureVector's
field order exactly and MUST stay in sync with internal/application/ml.FeatureOrder — the model
has no column names at inference time, only positional floats, and ml.LoadOnnxModel refuses to
load a model whose metadata sidecar disagrees.

Every feature is scale-free by construction (a ratio or a bounded index, never a raw price or
share count). That was a real fix, not a style choice: the original feature set fed raw
EMA/MACD/ATR in, so each carried the symbol's absolute price level, and since per-symbol
correction base rates range from 0.00 to 0.58 across the collected universe, the model could
score respectably by memorising "which symbol is this". It measurably did — see
domain/ml.FeatureVector's doc comment, and the symbol-base-rate baseline this script now computes
on every run to keep that failure mode visible instead of flattering.

Validation is a per-symbol time-ordered holdout with an embargo gap (not full walk-forward
validation, which is a separate, larger backtesting gap — see docs/backtesting.md), on whatever
history Atlas has actually collected (see PROJECT_CONTEXT.md's Implementation status notes).
Class balance, baselines and holdout metrics are always printed and written to the model's
metadata sidecar so a degenerate or overconfident model is visible, not hidden.
"""
import argparse
import json
import random
import sys
import time

import pandas as pd
from onnxmltools.convert import convert_xgboost
from onnxmltools.convert.common.data_types import FloatTensorType
from sklearn.metrics import accuracy_score, average_precision_score, brier_score_loss, roc_auc_score
from xgboost import XGBClassifier

FEATURE_ORDER = ["rsi", "ema_dist", "macd_pct", "tr_pct", "atr_pct", "rvol_20", "rvol_50", "volume"]
HOLDOUT_FRACTION = 0.2

# Bars to drop between each symbol's train and holdout segments. A label looks forward, so
# without this gap the last training rows' labels are computed from bars that live in the
# holdout — the train/holdout boundary leaks, and the holdout score is optimistic. The defaults
# match cmd/export-training-data's own label horizons: HorizonBars for correction, and
# HorizonBars + RecoveryHorizonBars for recovery, whose label can only resolve after the
# correction it is conditional on has already played out.
DEFAULT_EMBARGO_BARS = {"correction": 10, "recovery": 30}


def time_ordered_split(df: pd.DataFrame, holdout_fraction: float, embargo_bars: int):
    """Splits each symbol's rows by time (last `holdout_fraction` -> holdout), not randomly, and
    drops `embargo_bars` rows at the boundary so no training row's forward-looking label window
    reaches into a holdout row. A random shuffle split would leak future information wholesale;
    a time split without the embargo leaks it only at the seam, which is subtler and was the
    original version's actual bug.

    Symbols too short to give up an embargo gap are put entirely in train rather than split
    dishonestly — they still contribute signal, they just can't contribute a trustworthy holdout.
    """
    train_parts, holdout_parts, dropped = [], [], 0
    for _, group in df.sort_values("ts").groupby("symbol"):
        cut = max(1, int(len(group) * (1 - holdout_fraction)))
        train_end = cut - embargo_bars
        if train_end < 1 or cut >= len(group):
            train_parts.append(group)
            continue
        train_parts.append(group.iloc[:train_end])
        holdout_parts.append(group.iloc[cut:])
        dropped += cut - train_end
    holdout = pd.concat(holdout_parts) if holdout_parts else pd.DataFrame(columns=df.columns)
    return pd.concat(train_parts), holdout, dropped


def symbol_base_rate_scores(train_df: pd.DataFrame, holdout_df: pd.DataFrame):
    """ROC AUC and PR AUC of a predictor that uses no features at all — only each symbol's
    positive rate in the training data. This is the bar a real model has to clear, kept as a
    permanent part of every run because the original feature set failed against it (0.707 vs
    0.757) while looking fine on its own reported numbers.

    Note what this predictor *is*: one constant per symbol. It can rank instruments by how often
    they historically fell, and nothing else — it cannot say that a correction is likelier now
    than it was yesterday. Atlas exists to answer exactly that question on the 1h timeframe, so
    the baseline is a floor for "did the model learn anything", not a competing product.
    """
    if holdout_df["label"].nunique() < 2:
        return float("nan"), float("nan")
    rates = train_df.groupby("symbol")["label"].mean()
    predictions = holdout_df["symbol"].map(rates).fillna(train_df["label"].mean())
    return (roc_auc_score(holdout_df["label"], predictions),
            average_precision_score(holdout_df["label"], predictions))


def unseen_symbol_auc(df: pd.DataFrame, model_factory, seed: int = 42) -> float:
    """ROC AUC on instruments the model has never seen — train on ~75% of symbols, score the
    rest. This is the metric that actually answers "did it learn price dynamics or memorise this
    universe", and it is the one where the symbol-base-rate baseline scores exactly 0.5 by
    construction: it has no history for a symbol it has never met, so it can only emit a constant.

    That matters practically, not just philosophically. Atlas lets a user add any ticker to their
    watchlist, and the model is expected to score it from day one. A per-symbol frequency table
    cannot do that at all, so a model that beats 0.5 here is delivering something no lookup can,
    even while losing to that lookup on symbols with long history.
    """
    symbols = sorted(df["symbol"].unique())
    if len(symbols) < 8:
        return float("nan")
    rng = random.Random(seed)
    shuffled = symbols[:]
    rng.shuffle(shuffled)
    held = set(shuffled[: max(2, len(shuffled) // 4)])
    train_part, test_part = df[~df["symbol"].isin(held)], df[df["symbol"].isin(held)]
    if test_part["label"].nunique() < 2 or train_part["label"].nunique() < 2:
        return float("nan")
    model = model_factory()
    model.fit(train_part[FEATURE_ORDER].values, train_part["label"])
    return roc_auc_score(test_part["label"], model.predict_proba(test_part[FEATURE_ORDER].values)[:, 1])


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--input", default="training/data/features.csv")
    parser.add_argument("--output-dir", default="training/models")
    parser.add_argument("--version", default=None, help="defaults to a timestamp-based version")
    parser.add_argument("--target", choices=["correction", "recovery"], default="correction",
                        help="which label this CSV contains — affects the output model's "
                             "filename prefix, the metadata's label_definition text and the "
                             "default embargo width; the training procedure itself is identical")
    parser.add_argument("--embargo-bars", type=int, default=None,
                        help="bars dropped at each symbol's train/holdout boundary; defaults to "
                             "the label horizon for --target (correction 10, recovery 30) and "
                             "must be raised to match if you export with a longer horizon")
    args = parser.parse_args()

    embargo_bars = args.embargo_bars if args.embargo_bars is not None else DEFAULT_EMBARGO_BARS[args.target]

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
        print("if this lists atr_pct/rvol_20/rvol_50, the CSV predates the volatility features; if it lists ema_dist/macd_pct/tr_pct, it predates the scale-free feature "
              "change — re-run cmd/export-training-data to regenerate it", file=sys.stderr)
        return 1

    print(f"loaded {len(df)} rows across {df['symbol'].nunique()} symbols")
    class_counts = df["label"].value_counts().to_dict()
    print(f"label distribution: {class_counts}")
    if len(class_counts) < 2:
        print("only one label class present in the data; cannot train a classifier on this "
              "yet — collect more history or widen the correction threshold/horizon", file=sys.stderr)
        return 1

    train_df, holdout_df, embargoed = time_ordered_split(df, HOLDOUT_FRACTION, embargo_bars)
    print(f"split: {len(train_df)} train / {len(holdout_df)} holdout "
          f"({embargoed} rows dropped as a {embargo_bars}-bar embargo at the boundary)")
    if len(holdout_df) == 0 or holdout_df["label"].nunique() < 2:
        print("holdout split ended up empty or single-class; the dataset is too small for a "
              "meaningful per-symbol time-ordered split with a "
              f"{embargo_bars}-bar embargo yet", file=sys.stderr)
        return 1

    # .values (not the DataFrame directly): onnxmltools' XGBoost converter expects the
    # booster's internal feature names to follow XGBoost's default "f0", "f1", ... pattern,
    # which only happens when XGBoost is trained on a plain array — a named DataFrame makes
    # XGBoost store the real column names internally, which the converter can't map back to
    # ONNX input positions ("Unable to interpret 'macd', feature names should follow 'f%d'").
    X_train, y_train = train_df[FEATURE_ORDER].values, train_df["label"]
    X_holdout, y_holdout = holdout_df[FEATURE_ORDER].values, holdout_df["label"]

    # Positive rows heavily overlap: consecutive hourly bars around one drop event all carry the
    # same label, so the effective independent sample is several times smaller than the row
    # count. Hence deliberately conservative capacity (shallow trees, subsampling, L2) rather
    # than the deeper unregularised fit this used to run — with ~180 genuinely distinct drop
    # events behind ~1100 positive rows, the old settings were fitting noise.
    #
    # Deliberately NO scale_pos_weight, despite the ~1:8 imbalance making it the reflex choice.
    # Measured on this dataset, rebalancing bought almost nothing in ranking (ROC AUC 0.690 vs
    # 0.680, PR AUC 0.179 vs 0.173 — inside the noise at this sample size) and wrecked
    # calibration (Brier 0.211 vs 0.067; mean predicted probability 0.43 against a true rate of
    # 0.065). Atlas consumes this output *as a probability*, not as a ranking — the decision
    # engine blends Score = 1 - correction_probability and the dashboard shows the number to a
    # human — so a well-calibrated 0.68-AUC model is worth more here than a 0.69-AUC one that
    # systematically claims a correction is 6x likelier than it is.
    def make_model():
        return XGBClassifier(
            n_estimators=150, max_depth=3, learning_rate=0.05,
            subsample=0.8, colsample_bytree=0.8,
            min_child_weight=5, reg_lambda=2.0,
            eval_metric="aucpr", random_state=42,
        )

    model = make_model()
    model.fit(X_train, y_train)

    holdout_proba = model.predict_proba(X_holdout)[:, 1]
    holdout_pred = (holdout_proba >= 0.5).astype(int)
    auc = roc_auc_score(y_holdout, holdout_proba)
    average_precision = average_precision_score(y_holdout, holdout_proba)
    accuracy = accuracy_score(y_holdout, holdout_pred)
    positive_rate = float(y_holdout.mean())
    majority_accuracy = max(positive_rate, 1 - positive_rate)
    baseline_auc, baseline_pr = symbol_base_rate_scores(train_df, holdout_df)
    brier = brier_score_loss(y_holdout, holdout_proba)
    mean_predicted = float(holdout_proba.mean())

    print(f"holdout n={len(holdout_df)}, positive rate {positive_rate:.4f}")
    print(f"  PR AUC (avg prec): {average_precision:.4f}   (symbol-base-rate baseline "
          f"{baseline_pr:.4f}, no-skill floor {positive_rate:.4f})   <- the one that matters")
    print(f"  ROC AUC          : {auc:.4f}   (symbol-base-rate baseline {baseline_auc:.4f})")
    print(f"  accuracy@0.5     : {accuracy:.4f}   (majority-class baseline {majority_accuracy:.4f})")
    print(f"  Brier score      : {brier:.4f}   (mean predicted {mean_predicted:.4f} vs actual {positive_rate:.4f})")

    unseen_auc = unseen_symbol_auc(df, make_model)
    if unseen_auc == unseen_auc:
        print(f"  unseen-symbol AUC: {unseen_auc:.4f}   (base-rate lookup scores 0.5000 here by "
              f"construction — it has no history for a symbol it has never seen)")

    # The verdict is judged on PR AUC, not ROC AUC, and the distinction is not academic here.
    # Atlas alerts on the *top* of the ranking — a handful of "a correction may be coming" calls
    # — while ROC AUC is dominated by how well the vast, uneventful middle is ordered, which no
    # alert ever touches. Measured on real data the two disagree in exactly that way: the model
    # trails the baseline on ROC AUC (0.710 vs 0.748) while beating it on PR AUC (0.264 vs
    # 0.188), and at the 25 highest-scoring bars it was right 68% of the time against a 6.5%
    # base rate — a 10.5x lift, where the baseline managed 16%. Judging on ROC AUC would have
    # condemned a model that does the job it is actually deployed to do.
    beats_baseline = bool(average_precision > baseline_pr) if baseline_pr == baseline_pr else None
    if beats_baseline is False:
        print("\nWARNING: this model does NOT beat the symbol-base-rate baseline on PR AUC. It is "
              "reproducing per-symbol correction frequencies rather than finding the moments when "
              "a correction is actually likelier — which is what Atlas alerts on. Do not promote "
              "it from the Account page on the strength of its accuracy figure.", file=sys.stderr)

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
        "embargo_bars": embargo_bars,
        "embargoed_row_count": embargoed,
        "label_distribution": {str(k): int(v) for k, v in class_counts.items()},
        "holdout_auc": auc,
        "holdout_average_precision": average_precision,
        "holdout_accuracy": accuracy,
        "holdout_positive_rate": positive_rate,
        "majority_class_accuracy": majority_accuracy,
        "holdout_brier_score": brier,
        "holdout_mean_predicted": mean_predicted,
        "symbol_base_rate_auc": None if baseline_auc != baseline_auc else baseline_auc,
        "symbol_base_rate_average_precision": None if baseline_pr != baseline_pr else baseline_pr,
        "unseen_symbol_auc": None if unseen_auc != unseen_auc else unseen_auc,
        "beats_symbol_base_rate": beats_baseline,
        "label_definition": label_definition,
    }
    with open(metadata_path, "w") as f:
        json.dump(metadata, f, indent=2)

    print(f"wrote {onnx_path}")
    print(f"wrote {metadata_path}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
