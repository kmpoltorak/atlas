package backtesting

import (
	"testing"
	"time"

	analyzerUC "atlas/internal/application/analyzer"
	decisionUC "atlas/internal/application/decision"
	portfolioUC "atlas/internal/application/portfolio"
	"atlas/internal/domain/market"
	"atlas/internal/domain/portfolio"
)

func newOrchestrator() *decisionUC.Orchestrator {
	return decisionUC.NewOrchestrator(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), decisionUC.NewUseCase())
}

// uptrendBars builds a long, steadily rising price series so the real decision engine has a
// genuine reason to issue BUY signals during the simulation.
func uptrendBars(n int) []market.Bar {
	bars := make([]market.Bar, 0, n)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	price := 100.0
	for i := 0; i < n; i++ {
		price += 1.5
		bars = append(bars, market.Bar{
			Symbol: "AAPL", Open: price - 4, High: price + 2, Low: price - 5, Close: price,
			Volume: 1200, Timestamp: base.AddDate(0, 0, i),
		})
	}
	return bars
}

func TestRunRejectsInvalidInputs(t *testing.T) {
	uc := NewUseCase(newOrchestrator())
	bars := uptrendBars(30)

	if _, err := uc.Run("AAPL", bars, 10000, "", "v1"); err == nil {
		t.Fatal("expected error for missing strategy name")
	}
	if _, err := uc.Run("AAPL", bars, 0, "momentum", "v1"); err == nil {
		t.Fatal("expected error for non-positive initial capital")
	}
	if _, err := uc.Run("AAPL", bars[:10], 10000, "momentum", "v1"); err == nil {
		t.Fatal("expected error for too few bars")
	}
	uc = NewUseCase(nil)
	if _, err := uc.Run("AAPL", bars, 10000, "momentum", "v1"); err == nil {
		t.Fatal("expected error when orchestrator is not configured")
	}
}

func TestRunProducesRealTradesFromRealMarketData(t *testing.T) {
	uc := NewUseCase(newOrchestrator())
	bars := uptrendBars(40)

	result, err := uc.Run("AAPL", bars, 10000, "momentum", "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StrategyName != "momentum" || result.StrategyVersion != "v1" {
		t.Fatalf("unexpected strategy metadata: %+v", result)
	}
	if result.TradeCount == 0 {
		t.Fatal("expected at least one trade on a sustained uptrend")
	}
	if len(result.Trades) != result.TradeCount {
		t.Fatalf("TradeCount (%d) does not match len(Trades) (%d)", result.TradeCount, len(result.Trades))
	}
	for _, trade := range result.Trades {
		if trade.Symbol != "AAPL" {
			t.Fatalf("expected trade symbol AAPL, got %q", trade.Symbol)
		}
		if trade.Price <= 0 {
			t.Fatalf("expected a positive trade price, got %v", trade.Price)
		}
		if trade.Quantity <= 0 {
			t.Fatalf("expected a positive trade quantity, got %v", trade.Quantity)
		}
	}
	// A sustained uptrend bought early should show a real, non-hardcoded positive return —
	// this is the property that distinguishes real simulation from the old fixed-0.08 stub.
	if result.TotalReturn <= 0 {
		t.Fatalf("expected a positive total return on a sustained uptrend, got %v", result.TotalReturn)
	}
}

func TestRunDoesNotUseFutureData(t *testing.T) {
	uc := NewUseCase(newOrchestrator())

	base := uptrendBars(40)
	// Append one final bar with an extreme price spike that a lookahead bug would leak into
	// earlier decisions.
	spiked := append(append([]market.Bar{}, base...), market.Bar{
		Symbol: "AAPL", Open: 500, High: 900, Low: 480, Close: 850, Volume: 50000,
		Timestamp: base[len(base)-1].Timestamp.AddDate(0, 0, 1),
	})

	resultBase, err := uc.Run("AAPL", base, 10000, "momentum", "v1")
	if err != nil {
		t.Fatalf("unexpected error on base run: %v", err)
	}
	resultSpiked, err := uc.Run("AAPL", spiked, 10000, "momentum", "v1")
	if err != nil {
		t.Fatalf("unexpected error on spiked run: %v", err)
	}

	if len(resultSpiked.Trades) < len(resultBase.Trades) {
		t.Fatalf("spiked run produced fewer trades (%d) than base run (%d)", len(resultSpiked.Trades), len(resultBase.Trades))
	}
	for i, trade := range resultBase.Trades {
		other := resultSpiked.Trades[i]
		if !trade.Timestamp.Equal(other.Timestamp) || trade.Action != other.Action || trade.Price != other.Price {
			t.Fatalf("decision at trade %d changed after appending a future bar: base=%+v spiked=%+v", i, trade, other)
		}
	}
}

// TestRunDoesNotLeakLivePortfolioState guards against the bug documented in
// docs/backtesting.md: the backtest must judge HasPosition from its own simulated position,
// not from whatever the live Portfolio use case (shared with real trading) happens to hold.
// A live AAPL position would turn the sustained uptrend's opening BUY into ACCUMULATE if it
// leaked in; asserting the first trade is a real BUY proves it starts from a clean simulated
// slate regardless of live state.
func TestRunDoesNotLeakLivePortfolioState(t *testing.T) {
	livePortfolio := portfolioUC.NewUseCase()
	livePortfolio.Positions = []portfolio.Position{{Symbol: "AAPL", Quantity: 50, AverageCost: 80, CurrentPrice: 100}}
	orchestrator := decisionUC.NewOrchestrator(analyzerUC.NewUseCase(), livePortfolio, decisionUC.NewUseCase())

	uc := NewUseCase(orchestrator)
	bars := uptrendBars(40)

	result, err := uc.Run("AAPL", bars, 10000, "momentum", "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Trades) == 0 {
		t.Fatal("expected at least one trade on a sustained uptrend")
	}
	if result.Trades[0].Action != "BUY" {
		t.Fatalf("expected the opening trade to be BUY (simulated position starts empty), got %q — live portfolio state leaked in", result.Trades[0].Action)
	}
	// The live portfolio itself must be untouched by the simulation.
	if len(livePortfolio.Positions) != 1 || livePortfolio.Positions[0].Quantity != 50 {
		t.Fatalf("expected live portfolio to remain unmodified by the backtest, got %+v", livePortfolio.Positions)
	}
}

// TestRunOmitsMLEvaluationWithoutARealModel confirms no fabricated metric is produced when
// there's no real trained model loaded — the orchestrator here has no ML use case at all,
// matching most existing tests/call sites (see docs/decision_engine.md's nil-safe ML pattern).
func TestRunOmitsMLEvaluationWithoutARealModel(t *testing.T) {
	uc := NewUseCase(newOrchestrator())
	result, err := uc.Run("AAPL", uptrendBars(40), 10000, "momentum", "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MLEvaluation != nil {
		t.Fatalf("expected nil MLEvaluation without a loaded model, got %+v", result.MLEvaluation)
	}
}

func TestComputeMLEvaluationPerfectPredictions(t *testing.T) {
	trueLabels := []int{1, 1, 0, 0, 1, 0}
	predictedProbs := []float64{0.9, 0.8, 0.1, 0.2, 0.7, 0.3}

	eval := computeMLEvaluation(trueLabels, predictedProbs)
	if eval.SampleSize != 6 {
		t.Fatalf("expected sample size 6, got %d", eval.SampleSize)
	}
	if eval.Accuracy != 1 || eval.Precision != 1 || eval.Recall != 1 || eval.F1Score != 1 {
		t.Fatalf("expected perfect scores for perfectly separated predictions, got %+v", eval)
	}
	if eval.ROCAUC != 1 {
		t.Fatalf("expected ROC AUC 1 for perfectly separated predictions, got %v", eval.ROCAUC)
	}
}

func TestComputeMLEvaluationWorstCasePredictions(t *testing.T) {
	// Every positive scored lower than every negative — the model is perfectly wrong, which
	// should read as AUC 0 (not 0.5, which would mean "no better than chance").
	trueLabels := []int{1, 1, 0, 0}
	predictedProbs := []float64{0.1, 0.2, 0.8, 0.9}

	eval := computeMLEvaluation(trueLabels, predictedProbs)
	if eval.ROCAUC != 0 {
		t.Fatalf("expected ROC AUC 0 for perfectly inverted predictions, got %v", eval.ROCAUC)
	}
}

func TestRocAUCIsUndefinedWithOnlyOneClass(t *testing.T) {
	auc := rocAUC([]int{1, 1, 1}, []float64{0.9, 0.6, 0.3})
	if auc != 0.5 {
		t.Fatalf("expected 0.5 when only one class is present, got %v", auc)
	}
}

// TestTradingCostsAreCharged pins the one thing a zero-fee backtester gets wrong: a strategy
// that buys and never sells should end up holding strictly fewer shares than its capital would
// have bought at the market price, because the entry fee came out of the same cash.
func TestTradingCostsAreCharged(t *testing.T) {
	uc := NewUseCase(newOrchestrator())
	bars := uptrendBars(40)

	result, err := uc.Run("AAPL", bars, 10000, "momentum", "v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var bought bool
	for _, trade := range result.Trades {
		if trade.Action != "BUY" {
			continue
		}
		bought = true
		free := 10000 / trade.Price
		if trade.Quantity >= free {
			t.Fatalf("entry fee not charged: got %v shares, a free trade would buy %v", trade.Quantity, free)
		}
		if trade.Quantity < free*(1-2*tradeCostRate) {
			t.Fatalf("entry fee overcharged: got %v shares against %v at one fee", trade.Quantity, free)
		}
		break
	}
	if !bought {
		t.Fatal("expected at least one BUY to check the fee against")
	}
}
