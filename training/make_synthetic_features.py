#!/usr/bin/env python3
"""Generates a synthetic features CSV shaped exactly like cmd/export-training-data's output.

Used by scripts/trainer-smoke-test.sh to run a real end-to-end training pass in CI, without
needing collected market history. It is not a data generator for research — the labels are
deliberately learnable-but-noisy so the run completes and the metrics are finite, and nothing
about the numbers means anything.

This exists because the smoke test used to check only that train.py *parsed*. A NameError inside
a function body — which is exactly what shipped on 2026-09-06, `roc_auc_score` undefined inside
symbol_base_rate_scores — is invisible to py_compile and only appears when that line runs.
"""
import argparse
import csv
import random
from datetime import datetime, timedelta, timezone

COLUMNS = ["symbol", "ts", "rsi", "ema_dist", "macd_pct", "tr_pct", "atr_pct",
           "rvol_20", "rvol_50", "volume", "label", "label_end"]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True)
    parser.add_argument("--symbols", type=int, default=12)
    parser.add_argument("--rows-per-symbol", type=int, default=400)
    parser.add_argument("--horizon-hours", type=int, default=10)
    parser.add_argument("--seed", type=int, default=7)
    args = parser.parse_args()

    rng = random.Random(args.seed)
    start = datetime(2026, 1, 1, tzinfo=timezone.utc)

    with open(args.output, "w", newline="") as handle:
        writer = csv.writer(handle)
        writer.writerow(COLUMNS)
        for s in range(args.symbols):
            symbol = f"SYN{s:02d}"
            # A per-symbol base rate, so symbol_base_rate_scores has something to rank by — the
            # baseline it computes is a real part of every run and must not divide by zero.
            base_rate = 0.05 + 0.30 * (s / max(1, args.symbols - 1))
            for i in range(args.rows_per_symbol):
                ts = start + timedelta(hours=i)
                rsi = rng.uniform(10, 90)
                # Weakly informative: high RSI raises correction probability. Enough signal for
                # AUC to be finite and above chance, far too little to mean anything.
                probability = min(0.95, max(0.02, base_rate + (rsi - 50) / 400))
                writer.writerow([
                    symbol,
                    ts.isoformat().replace("+00:00", "Z"),
                    f"{rsi:.4f}",
                    f"{rng.uniform(-0.05, 0.05):.6f}",
                    f"{rng.uniform(-0.02, 0.02):.6f}",
                    f"{rng.uniform(0.001, 0.03):.6f}",
                    f"{rng.uniform(0.001, 0.04):.6f}",
                    f"{rng.uniform(0.005, 0.05):.6f}",
                    f"{rng.uniform(0.005, 0.05):.6f}",
                    f"{rng.uniform(1e5, 1e7):.1f}",
                    1 if rng.random() < probability else 0,
                    (ts + timedelta(hours=args.horizon_hours)).isoformat().replace("+00:00", "Z"),
                ])
    print(f"wrote {args.symbols * args.rows_per_symbol} synthetic rows to {args.output}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
