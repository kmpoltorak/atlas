#!/usr/bin/env python3
"""Tests for train.py's train/holdout split — the one piece of the training pipeline whose
failure mode is silent. A leaky split does not crash or look wrong; it just reports a holdout
score that is too good, which is exactly the number a human then trusts.

Deliberately stdlib unittest and pandas only, no pytest: the split lives in its own module
(split.py) precisely so this runs without the full training stack.

Run: python -m unittest discover -s training -p 'test_*.py'
"""
import sys
import unittest
from pathlib import Path

import pandas as pd

sys.path.insert(0, str(Path(__file__).parent))

from split import time_ordered_split  # noqa: E402


def frame(rows):
    """rows: (symbol, ts, label_end, label). Timestamps are ISO strings, as in the exporter CSV."""
    return pd.DataFrame(
        [{"symbol": s, "ts": ts, "label_end": le, "label": lbl} for s, ts, le, lbl in rows]
    )


def hours(symbol, start_hour, count, horizon=2, label=0):
    return [
        (
            symbol,
            f"2026-01-01T{start_hour + i:02d}:00:00Z",
            f"2026-01-01T{start_hour + i + horizon:02d}:00:00Z",
            label,
        )
        for i in range(count)
    ]


class TimeOrderedSplitTest(unittest.TestCase):
    def test_boundary_is_global_not_per_symbol(self):
        """The bug this replaced: symbols with different history lengths were each cut at their
        own row-count fraction, so a short symbol's training rows could postdate a long symbol's
        holdout. Every holdout row must start at or after every training row's boundary."""
        df = frame(hours("LONG", 0, 20) + hours("SHORT", 10, 6))

        train, holdout, _ = time_ordered_split(df, 0.25, embargo_bars=0)

        self.assertFalse(train.empty)
        self.assertFalse(holdout.empty)
        self.assertLess(train["ts"].max(), holdout["ts"].min())

    def test_no_training_label_reaches_into_the_holdout(self):
        """A label looks forward. A training row whose label_end lands past the boundary was
        computed from bars the holdout also contains — that is the leak the embargo used to only
        partly cover."""
        df = frame(hours("AAA", 0, 16, horizon=4) + hours("BBB", 0, 16, horizon=4))

        train, holdout, _ = time_ordered_split(df, 0.25, embargo_bars=0)

        boundary = holdout["ts"].min()
        self.assertTrue((train["label_end"] < boundary).all())

    def test_rows_are_never_double_counted(self):
        df = frame(hours("AAA", 0, 20))

        train, holdout, dropped = time_ordered_split(df, 0.2, embargo_bars=0)

        self.assertEqual(len(train) + len(holdout) + dropped, len(df))
        self.assertGreater(dropped, 0)  # the purge did something, rather than reporting a clean split

    def test_embargo_only_ever_removes_more_training_rows(self):
        df = frame(hours("AAA", 0, 20) + hours("BBB", 0, 20))

        without, holdout_a, _ = time_ordered_split(df, 0.2, embargo_bars=0)
        with_embargo, holdout_b, _ = time_ordered_split(df, 0.2, embargo_bars=3)

        self.assertLess(len(with_embargo), len(without))
        self.assertEqual(len(holdout_a), len(holdout_b))

    def test_label_ending_before_its_own_timestamp_is_rejected(self):
        df = frame([("AAA", "2026-01-01T05:00:00Z", "2026-01-01T01:00:00Z", 0)] + hours("AAA", 6, 10))

        with self.assertRaises(ValueError):
            time_ordered_split(df, 0.2, embargo_bars=0)

    def test_missing_label_end_is_rejected_rather_than_guessed(self):
        """An old CSV exported before label_end existed must fail loudly: the row count alone
        cannot establish a time-safe split, which is what the previous version assumed."""
        df = frame(hours("AAA", 0, 10))
        df["label_end"] = None

        with self.assertRaises(ValueError):
            time_ordered_split(df, 0.2, embargo_bars=0)

    def test_nonsense_parameters_are_rejected(self):
        df = frame(hours("AAA", 0, 10))

        for fraction, embargo in [(0.0, 0), (1.0, 0), (-0.5, 0), (0.2, -1)]:
            with self.subTest(fraction=fraction, embargo=embargo):
                with self.assertRaises(ValueError):
                    time_ordered_split(df, fraction, embargo)

    def test_single_timestamp_dataset_is_rejected(self):
        df = frame([("AAA", "2026-01-01T00:00:00Z", "2026-01-01T02:00:00Z", 0)] * 4)

        with self.assertRaises(ValueError):
            time_ordered_split(df, 0.2, embargo_bars=0)


if __name__ == "__main__":
    unittest.main()
