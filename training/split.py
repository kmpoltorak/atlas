#!/usr/bin/env python3
"""The train/holdout split, kept in its own module so it can be tested without the training stack.

train.py imports xgboost, sklearn and onnxmltools at module scope — roughly 500MB of
dependencies. Splitting the data is the part that most needs testing (a leaky split does not
crash or look wrong, it just reports a holdout score that is too good), and it needs pandas and
nothing else.

The previous arrangement deferred those heavy imports into function bodies so train.py itself
stayed importable. That worked until a function referenced a name whose lazy import lived in a
different function: `symbol_base_rate_scores` raised `NameError: roc_auc_score` mid-run, after
the export and the split had already completed. A module boundary cannot fail that way — a name
is either imported at the top of the file or it is not.
"""
import pandas as pd

HOLDOUT_FRACTION = 0.2

# Bars to drop between each symbol's train and holdout segments. A label looks forward, so
# without this gap the last training rows' labels are computed from bars that live in the
# holdout — the train/holdout boundary leaks, and the holdout score is optimistic. The defaults
# match cmd/export-training-data's own label horizons: HorizonBars for correction, and
# HorizonBars + RecoveryHorizonBars for recovery, whose label can only resolve after the
# correction it is conditional on has already played out.
DEFAULT_EMBARGO_BARS = {"correction": 10, "recovery": 30}


def time_ordered_split(df: pd.DataFrame, holdout_fraction: float, embargo_bars: int):
    """Use a common timestamp boundary and purge labels crossing it across all symbols.

    label_end comes from the Go exporter and includes the forward horizon. Old CSVs
    must be re-exported; row counts alone cannot establish a time-safe split.
    """
    if not 0 < holdout_fraction < 1 or embargo_bars < 0:
        raise ValueError("invalid holdout fraction or embargo")
    ordered = df.copy()
    ordered["ts"] = pd.to_datetime(ordered["ts"], utc=True, errors="raise")
    ordered["label_end"] = pd.to_datetime(ordered["label_end"], utc=True, errors="raise")
    if ordered[["ts", "label_end"]].isna().any().any() or (ordered["label_end"] <= ordered["ts"]).any():
        raise ValueError("labels must end after their feature timestamps")
    times = sorted(ordered["ts"].unique())
    if len(times) < 2:
        raise ValueError("not enough distinct timestamps")
    cut = min(len(times)-1, max(1, int(len(times) * (1-holdout_fraction))))
    boundary = times[cut]
    train = ordered[(ordered["ts"] < boundary) & (ordered["label_end"] < boundary)].copy()
    # An optional extra per-symbol embargo can only remove more training rows.
    if embargo_bars:
        train = train.sort_values("ts").groupby("symbol", group_keys=False).head(-embargo_bars)
    holdout = ordered[ordered["ts"] >= boundary].copy()
    dropped = len(ordered)-len(train)-len(holdout)
    return train, holdout, dropped
