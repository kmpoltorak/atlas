package alerts

import (
	"context"
	"errors"
	"testing"
	"time"

	analyzerUC "atlas/internal/application/analyzer"
	collectorUC "atlas/internal/application/collector"
	decisionUC "atlas/internal/application/decision"
	portfolioUC "atlas/internal/application/portfolio"
	watchlistUC "atlas/internal/application/watchlist"
	"atlas/internal/domain/market"
)

type stubProvider struct {
	bars map[string][]market.Bar
}

func (s stubProvider) FetchBars(_ context.Context, symbol string, window int) ([]market.Bar, error) {
	return s.bars[symbol], nil
}

func (s stubProvider) FetchInstrumentInfo(_ context.Context, symbol string) (market.InstrumentInfo, error) {
	return market.InstrumentInfo{Symbol: symbol}, nil
}

func (s stubProvider) FetchQuote(_ context.Context, symbol string) (market.Quote, error) {
	return market.Quote{Symbol: symbol}, nil
}

// failingProvider errors for any symbol in failFor, succeeds (with empty bars) otherwise — used
// to exercise OnProviderCall's success/error branching.
type failingProvider struct {
	failFor map[string]bool
}

func (f failingProvider) FetchBars(_ context.Context, symbol string, window int) ([]market.Bar, error) {
	if f.failFor[symbol] {
		return nil, errors.New("provider unavailable")
	}
	return flatBars(symbol), nil
}

func (f failingProvider) FetchInstrumentInfo(_ context.Context, symbol string) (market.InstrumentInfo, error) {
	return market.InstrumentInfo{Symbol: symbol}, nil
}

func (f failingProvider) FetchQuote(_ context.Context, symbol string) (market.Quote, error) {
	return market.Quote{Symbol: symbol}, nil
}

// uptrendBars is a rising series long enough for the decision engine's momentum term to have a
// reading: it needs more than that term's lookback window, and the per-bar returns must vary,
// since a perfectly uniform climb has zero realized volatility to normalize against and reads as
// no signal at all. Volume is flat at the series average so volume confirmation stays at full
// weight. The previous three-bar version scored strongly only because the term it fed used to be
// a step function on one bar's open-to-close move.
func uptrendBars(symbol string) []market.Bar {
	bars := make([]market.Bar, 0, 30)
	base := time.Now().Add(-30 * time.Hour)
	price := 100.0
	for i := 0; i < 30; i++ {
		if i%2 == 0 {
			price *= 1.015
		} else {
			price *= 1.005
		}
		bars = append(bars, market.Bar{
			Symbol: symbol, Open: price / 1.004, High: price * 1.002, Low: price / 1.008,
			Close: price, Volume: 1200, Timestamp: base.Add(time.Duration(i) * time.Hour),
		})
	}
	return bars
}

func flatBars(symbol string) []market.Bar {
	return []market.Bar{{Symbol: symbol, Open: 100, High: 100, Low: 100, Close: 100, Volume: 100, Timestamp: time.Now()}}
}

func TestEvaluateSurfacesNonHoldAlerts(t *testing.T) {
	provider := stubProvider{bars: map[string][]market.Bar{
		"AAPL": uptrendBars("AAPL"),
		"MSFT": flatBars("MSFT"),
	}}

	watchlist := watchlistUC.NewUseCase()
	_ = watchlist.Add("AAPL", "2026-08-05T00:00:00Z")
	_ = watchlist.Add("MSFT", "2026-08-05T00:00:00Z")

	collector := &collectorUC.UseCase{Provider: provider}
	orchestrator := decisionUC.NewOrchestrator(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), decisionUC.NewUseCase())

	uc := NewUseCase(watchlist, collector, orchestrator, 30)
	result, err := uc.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected exactly one alert, got %d: %+v", len(result), result)
	}
	if result[0].Recommendation.Symbol != "AAPL" {
		t.Fatalf("expected alert for AAPL, got %q", result[0].Recommendation.Symbol)
	}
	if result[0].Recommendation.Action != "BUY" {
		t.Fatalf("expected BUY alert, got %q", result[0].Recommendation.Action)
	}
	if result[0].TriggeredAt == "" {
		t.Fatal("expected TriggeredAt to be set")
	}
	if len(result[0].Bars) == 0 {
		t.Fatal("expected Bars to carry the bars the recommendation was built from, for indicator-snapshot persistence")
	}
}

func TestEvaluateReturnsEmptyWhenWatchlistIsEmpty(t *testing.T) {
	watchlist := watchlistUC.NewUseCase()
	collector := &collectorUC.UseCase{Provider: stubProvider{}}
	orchestrator := decisionUC.NewOrchestrator(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), decisionUC.NewUseCase())

	uc := NewUseCase(watchlist, collector, orchestrator, 30)
	result, err := uc.Evaluate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("expected no alerts, got %d", len(result))
	}
}

// TestEvaluateReportsProviderCallOutcomes confirms the fix for provider failures during the
// background alert-poll loop being invisible outside logs: OnProviderCall must fire once per
// symbol that actually reached the live provider, with the right success/error outcome.
func TestEvaluateReportsProviderCallOutcomes(t *testing.T) {
	provider := failingProvider{failFor: map[string]bool{"BAD": true}}

	watchlist := watchlistUC.NewUseCase()
	_ = watchlist.Add("GOOD", "2026-08-05T00:00:00Z")
	_ = watchlist.Add("BAD", "2026-08-05T00:00:00Z")

	collector := &collectorUC.UseCase{Provider: provider}
	orchestrator := decisionUC.NewOrchestrator(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), decisionUC.NewUseCase())

	uc := NewUseCase(watchlist, collector, orchestrator, 30)
	outcomes := map[string]int{}
	uc.OnProviderCall = func(outcome string) { outcomes[outcome]++ }

	if _, err := uc.Evaluate(context.Background()); err == nil {
		t.Fatal("expected an aggregated error from the BAD symbol's failed fetch")
	}
	if outcomes["success"] != 1 || outcomes["error"] != 1 {
		t.Fatalf("expected exactly one success and one error, got %+v", outcomes)
	}
}
