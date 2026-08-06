package persistence

import (
	"testing"
	"time"

	"atlas/internal/domain/backtesting"
)

func TestSaveAndListBacktestRuns(t *testing.T) {
	db := connectTestDB(t)

	started := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	finished := started.AddDate(0, 6, 0)
	result := backtesting.StrategyResult{
		Symbol:          "TESTBT",
		InitialCapital:  10000,
		StrategyName:    "atlas-orchestrator",
		StrategyVersion: "v1",
		StartedAt:       started,
		FinishedAt:      finished,
		TotalReturn:     0.12,
		SharpeRatio:     1.1,
		TradeCount:      2,
		WinningTrades:   1,
		LosingTrades:    1,
		WinRate:         0.5,
		Trades: []backtesting.Trade{
			{Timestamp: started.AddDate(0, 1, 0), Symbol: "TESTBT", Action: "BUY", Price: 100, Quantity: 10, Confidence: 0.78},
		},
		MLEvaluation: &backtesting.MLEvaluation{SampleSize: 40, Accuracy: 0.6, ROCAUC: 0.65},
	}

	if err := db.SaveBacktestRun(result); err != nil {
		t.Fatalf("SaveBacktestRun failed: %v", err)
	}

	runs, err := db.ListBacktestRuns("TESTBT", 10)
	if err != nil {
		t.Fatalf("ListBacktestRuns failed: %v", err)
	}
	if len(runs) == 0 {
		t.Fatal("expected at least one persisted run")
	}

	latest := runs[0]
	if latest.Symbol != "TESTBT" || latest.StrategyName != "atlas-orchestrator" || latest.StrategyVersion != "v1" {
		t.Fatalf("unexpected run metadata: %+v", latest)
	}
	if latest.ResultSummary.TotalReturn != 0.12 || latest.ResultSummary.TradeCount != 2 {
		t.Fatalf("unexpected result summary: %+v", latest.ResultSummary)
	}
	if len(latest.ResultSummary.Trades) != 1 || latest.ResultSummary.Trades[0].Action != "BUY" {
		t.Fatalf("expected round-tripped trade detail, got %+v", latest.ResultSummary.Trades)
	}
	if latest.ResultSummary.MLEvaluation == nil || latest.ResultSummary.MLEvaluation.SampleSize != 40 {
		t.Fatalf("expected round-tripped ML evaluation, got %+v", latest.ResultSummary.MLEvaluation)
	}
}

func TestSaveBacktestRunAllowsMultipleRowsPerSymbol(t *testing.T) {
	db := connectTestDB(t)

	countForSymbol := func() int {
		var count int
		if err := db.db.Get(&count, `
			SELECT COUNT(*) FROM backtest_runs r
			JOIN symbols s ON s.id = r.symbol_id
			WHERE s.ticker = $1`, "TESTBTHIST"); err != nil {
			t.Fatalf("failed to count backtest runs: %v", err)
		}
		return count
	}
	before := countForSymbol()

	for i := 0; i < 2; i++ {
		result := backtesting.StrategyResult{
			Symbol: "TESTBTHIST", InitialCapital: 10000, StrategyName: "momentum", StrategyVersion: "v1",
			StartedAt: time.Now(), FinishedAt: time.Now(),
		}
		if err := db.SaveBacktestRun(result); err != nil {
			t.Fatalf("SaveBacktestRun failed: %v", err)
		}
	}

	if got := countForSymbol() - before; got != 2 {
		t.Fatalf("expected 2 new append-only rows, got %d", got)
	}
}
