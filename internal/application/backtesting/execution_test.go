package backtesting

import (
	analyzer "atlas/internal/application/analyzer"
	decision "atlas/internal/application/decision"
	domain "atlas/internal/domain/decision"
	"atlas/internal/domain/market"
	"context"
	"testing"
	"time"
)

func TestTradesExecuteAtNextOpen(t *testing.T) {
	thresholds := domain.DefaultThresholds()
	thresholds.StrongBullish = 0
	u := NewUseCase(decision.NewOrchestrator(analyzer.NewUseCase(), nil, decision.NewUseCaseWithThresholds(thresholds)))
	bars := make([]market.Bar, 20)
	for i := range bars {
		bars[i] = market.Bar{Timestamp: time.Date(2026, 1, 1, i, 0, 0, 0, time.UTC), Open: 200, High: 220, Low: 100, Close: 110, Volume: 100}
	}
	result, err := u.Run("AAPL", bars, 1000, "test", "v2")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Trades) == 0 || result.Trades[0].Price != buyFill(200) || !result.Trades[0].Timestamp.Equal(bars[minLookback].Timestamp) {
		t.Fatalf("wrong execution: %+v", result.Trades)
	}
	if result.TotalReturn >= 0 {
		t.Fatal("execution did not account for open-to-close loss")
	}
}

func TestDailyEquityDiscardsIntradaySamplingFrequency(t *testing.T) {
	day := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	bars := []market.Bar{{Timestamp: day}, {Timestamp: day.Add(time.Hour)}, {Timestamp: day.Add(24 * time.Hour)}, {Timestamp: day.Add(25 * time.Hour)}}
	got := dailyEquity(bars, []float64{100, 105, 110, 90, 95})
	want := []float64{100, 110, 95}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatal(got)
		}
	}
}

func TestBacktestHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	u := NewUseCase(decision.NewOrchestrator(analyzer.NewUseCase(), nil, decision.NewUseCase())).WithContext(ctx)
	if _, err := u.Run("AAPL", uptrendBars(40), 1000, "test", "v2"); err != context.Canceled {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

// A buy pays above the open and a sell receives below it, so a round trip crosses the spread
// twice — the cost a same-price fill silently hands back to the strategy.
func TestFillsCrossTheSpreadInBothDirections(t *testing.T) {
	if buyFill(100) <= 100 || sellFill(100) >= 100 {
		t.Fatalf("fills must move against the trade: buy %v, sell %v", buyFill(100), sellFill(100))
	}
	if fillPrice(100, "BUY") != buyFill(100) || fillPrice(100, "SELL") != sellFill(100) {
		t.Fatal("fillPrice picked the wrong side of the spread")
	}
}

// The annualization factor comes from the curve's own dates. The same equity path sampled
// hourly and daily covers the same calendar span, so it must annualize the same way — which a
// hardcoded sqrt(252) got wrong by a factor of sqrt(24) for hourly bars.
func TestObservationsPerYearFollowsTheCalendarNotTheBarCount(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Weekdays only, one calendar year: ~261 sessions, the shape a real equity curve has.
	var sessions []market.Bar
	for day := start; day.Before(start.AddDate(1, 0, 0)); day = day.AddDate(0, 0, 1) {
		if day.Weekday() != time.Saturday && day.Weekday() != time.Sunday {
			sessions = append(sessions, market.Bar{Timestamp: day})
		}
	}
	if got := observationsPerYear(sessions, len(sessions)); got < 245 || got > 275 {
		t.Fatalf("a year of weekday sessions should annualize near 252-261, got %v", got)
	}

	hourly := make([]market.Bar, 24*365)
	for i := range hourly {
		hourly[i] = market.Bar{Timestamp: start.Add(time.Duration(i) * time.Hour)}
	}
	// Sampled hourly but reduced to one observation per day, the factor must stay daily-scale.
	if got := observationsPerYear(hourly, 365); got < 340 || got > 380 {
		t.Fatalf("365 daily observations over one year should annualize near 365, got %v", got)
	}

	// Too short to measure: fall back rather than annualize a two-week sample.
	if got := observationsPerYear(sessions[:5], 5); got != 252 {
		t.Fatalf("a five-day sample should fall back to 252, got %v", got)
	}
}
