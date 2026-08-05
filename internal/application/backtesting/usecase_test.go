package backtesting

import (
	"testing"
	"time"

	analyzerUC "atlas/internal/application/analyzer"
	decisionUC "atlas/internal/application/decision"
	portfolioUC "atlas/internal/application/portfolio"
	"atlas/internal/domain/market"
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
