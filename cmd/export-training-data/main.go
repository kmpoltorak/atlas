// Command export-training-data reads every symbol's persisted price history from TimescaleDB
// and writes a CSV of (feature vector, label) rows for training/train.py to consume. It reuses
// the exact same analyzer-backed feature computation the live inference path uses
// (ml.BuildFeatureVector), so the training data and live features can never drift apart —
// Python never reimplements the indicator math, nor the scale-free normalisation
// BuildFeatureVector applies on top of it (see internal/domain/ml.FeatureVector).
//
// -label-type switches which target this exports: "correction" (default, every bar gets a
// label) or "recovery" (only bars where a correction actually occurred get a label — see
// ml.ComputeRecoveryLabel's doc comment for why recovery is conditional on correction).
package main

import (
	"context"
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	analyzerUC "atlas/internal/application/analyzer"
	mlUC "atlas/internal/application/ml"
	"atlas/internal/config"
	"atlas/internal/domain/market"
	"atlas/internal/infrastructure/persistence"
)

func main() {
	labelType := flag.String("label-type", "correction", `label to export: "correction" or "recovery"`)
	outputPath := flag.String("output", "", "CSV output path (defaults to training/data/features.csv, or training/data/recovery.csv for -label-type=recovery)")
	horizonBars := flag.Int("horizon-bars", mlUC.DefaultCorrectionLabelParams().HorizonBars, "bars ahead to look for a correction")
	thresholdPct := flag.Float64("threshold-pct", mlUC.DefaultCorrectionLabelParams().ThresholdPct, "drop fraction counted as a correction (0.05 = 5%)")
	recoveryHorizonBars := flag.Int("recovery-horizon-bars", mlUC.DefaultRecoveryLabelParams().RecoveryHorizonBars, "bars after the correction breach to look for a recovery (only used with -label-type=recovery)")
	flag.Parse()

	correctionParams := mlUC.CorrectionLabelParams{HorizonBars: *horizonBars, ThresholdPct: *thresholdPct}
	recoveryParams := mlUC.RecoveryLabelParams{CorrectionParams: correctionParams, RecoveryHorizonBars: *recoveryHorizonBars}

	var computeLabel func(bars []market.Bar, i int) (label int, ok bool)
	switch *labelType {
	case "correction":
		computeLabel = func(bars []market.Bar, i int) (int, bool) {
			return mlUC.ComputeCorrectionLabel(bars, i, correctionParams)
		}
		if *outputPath == "" {
			*outputPath = "training/data/features.csv"
		}
	case "recovery":
		computeLabel = func(bars []market.Bar, i int) (int, bool) { return mlUC.ComputeRecoveryLabel(bars, i, recoveryParams) }
		if *outputPath == "" {
			*outputPath = "training/data/recovery.csv"
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown -label-type %q: must be \"correction\" or \"recovery\"\n", *labelType)
		os.Exit(1)
	}

	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dsn := persistence.DSN(cfg.DatabaseHost, cfg.DatabasePort, cfg.DatabaseName, cfg.DatabaseUser, cfg.DatabasePassword, cfg.DatabaseSSLMode)
	// A one-shot exporter reads one table at a time; the pool defaults are more than it needs.
	storage, err := persistence.NewTimescaleDB(ctx, dsn, persistence.PoolLimits{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect to timescaledb:", err)
		os.Exit(1)
	}
	defer storage.Close()

	symbols, err := storage.ListSymbolsWithBars()
	if err != nil {
		fmt.Fprintln(os.Stderr, "list symbols:", err)
		os.Exit(1)
	}
	if len(symbols) == 0 {
		fmt.Fprintln(os.Stderr, "no symbols with persisted bars found; collect some history first (e.g. GET /prices) before exporting training data")
		os.Exit(1)
	}

	if err := os.MkdirAll(filepath.Dir(*outputPath), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "create output directory:", err)
		os.Exit(1)
	}
	file, err := os.Create(*outputPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create output file:", err)
		os.Exit(1)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()
	// Column names must match ml.FeatureOrder / train.py's FEATURE_ORDER exactly — train.py
	// selects its feature columns by name, so a mismatch here fails the run loudly rather than
	// silently training on the wrong columns.
	if err := writer.Write([]string{"symbol", "ts", "rsi", "ema_dist", "macd_pct", "tr_pct", "atr_pct", "rvol_20", "rvol_50", "volume", "label", "label_end"}); err != nil {
		fmt.Fprintln(os.Stderr, "write header:", err)
		os.Exit(1)
	}

	analyzer := analyzerUC.NewUseCase()
	rows := 0
	for _, symbol := range symbols {
		bars, err := storage.GetBars(symbol)
		if err != nil {
			fmt.Fprintf(os.Stderr, "get bars for %s: %v\n", symbol, err)
			continue
		}
		for i := mlUC.MinFeatureLookback; i < len(bars); i++ {
			label, ok := computeLabel(bars, i)
			if !ok {
				continue
			}
			labelEnd := i + *horizonBars
			if *labelType == "recovery" {
				labelEnd += *recoveryHorizonBars
			}
			if labelEnd >= len(bars) {
				continue
			}
			feature := mlUC.BuildFeatureVector(analyzer, symbol, bars[:i+1])
			if err := writer.Write([]string{
				symbol,
				bars[i].Timestamp.UTC().Format(time.RFC3339),
				strconv.FormatFloat(feature.RSI, 'f', -1, 64),
				strconv.FormatFloat(feature.EMADist, 'f', -1, 64),
				strconv.FormatFloat(feature.MACDPct, 'f', -1, 64),
				strconv.FormatFloat(feature.TRPct, 'f', -1, 64),
				strconv.FormatFloat(feature.ATRPct, 'f', -1, 64),
				strconv.FormatFloat(feature.RealizedVol20, 'f', -1, 64),
				strconv.FormatFloat(feature.RealizedVol50, 'f', -1, 64),
				strconv.FormatFloat(feature.Volume, 'f', -1, 64),
				strconv.Itoa(label),
				bars[labelEnd].Timestamp.Add(time.Hour).UTC().Format(time.RFC3339),
			}); err != nil {
				fmt.Fprintln(os.Stderr, "write row:", err)
				os.Exit(1)
			}
			rows++
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		fmt.Fprintln(os.Stderr, "flush CSV:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %d rows across %d symbols to %s\n", rows, len(symbols), *outputPath)
}
