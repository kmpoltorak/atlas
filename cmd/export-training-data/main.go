// Command export-training-data reads every symbol's persisted price history from TimescaleDB
// and writes a CSV of (feature vector, correction label) rows for training/train.py to consume.
// It reuses the exact same analyzer-backed feature computation the live inference path uses
// (ml.BuildFeatureVector), so the training data and live features can never drift apart —
// Python never reimplements RSI/EMA/MACD/ATR/Volume math.
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
	"atlas/internal/infrastructure/persistence"
)

func main() {
	outputPath := flag.String("output", "training/data/features.csv", "CSV output path")
	horizonBars := flag.Int("horizon-bars", mlUC.DefaultCorrectionLabelParams().HorizonBars, "bars ahead to look for a correction")
	thresholdPct := flag.Float64("threshold-pct", mlUC.DefaultCorrectionLabelParams().ThresholdPct, "drop fraction counted as a correction (0.05 = 5%)")
	flag.Parse()

	params := mlUC.CorrectionLabelParams{HorizonBars: *horizonBars, ThresholdPct: *thresholdPct}

	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dsn := persistence.DSN(cfg.DatabaseHost, cfg.DatabasePort, cfg.DatabaseName, cfg.DatabaseUser, cfg.DatabasePassword)
	storage, err := persistence.NewTimescaleDB(ctx, dsn)
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
	if err := writer.Write([]string{"symbol", "ts", "rsi", "ema", "macd", "atr", "volume", "label"}); err != nil {
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
			label, ok := mlUC.ComputeCorrectionLabel(bars, i, params)
			if !ok {
				continue
			}
			feature := mlUC.BuildFeatureVector(analyzer, symbol, bars[:i+1])
			if err := writer.Write([]string{
				symbol,
				bars[i].Timestamp.UTC().Format(time.RFC3339),
				strconv.FormatFloat(feature.RSI, 'f', -1, 64),
				strconv.FormatFloat(feature.EMA, 'f', -1, 64),
				strconv.FormatFloat(feature.MACD, 'f', -1, 64),
				strconv.FormatFloat(feature.ATR, 'f', -1, 64),
				strconv.FormatFloat(feature.Volume, 'f', -1, 64),
				strconv.Itoa(label),
			}); err != nil {
				fmt.Fprintln(os.Stderr, "write row:", err)
				os.Exit(1)
			}
			rows++
		}
	}

	fmt.Fprintf(os.Stderr, "wrote %d rows across %d symbols to %s\n", rows, len(symbols), *outputPath)
}
