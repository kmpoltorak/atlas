package analyzer

import (
	"testing"
	"time"

	"atlas/internal/domain/market"
)

func TestComputeEMAProducesStableSequence(t *testing.T) {
	uc := NewUseCase()
	bars := []market.Bar{
		{Close: 100, Timestamp: time.Now()},
		{Close: 110, Timestamp: time.Now().Add(time.Minute)},
		{Close: 120, Timestamp: time.Now().Add(2 * time.Minute)},
	}

	result, err := uc.ComputeEMA("AAPL", bars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != len(bars) {
		t.Fatalf("expected %d ema values, got %d", len(bars), len(result))
	}
	if result[0] != 100 {
		t.Fatalf("expected first EMA to remain the initial close, got %v", result[0])
	}
}

func TestComputeVolumeReturnsNormalizedValues(t *testing.T) {
	uc := NewUseCase()
	bars := []market.Bar{
		{Volume: 100},
		{Volume: 200},
		{Volume: 300},
	}

	result, err := uc.ComputeVolume("AAPL", bars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != len(bars) {
		t.Fatalf("expected %d volume values, got %d", len(bars), len(result))
	}
	if result[0] != 0.5 {
		t.Fatalf("expected first normalized volume to be 0.5, got %v", result[0])
	}
}

func TestComputeOBVAccumulatesOnCloseDirection(t *testing.T) {
	uc := NewUseCase()
	bars := []market.Bar{
		{Close: 100, Volume: 1000},
		{Close: 105, Volume: 500}, // up: +500
		{Close: 105, Volume: 300}, // unchanged: +0
		{Close: 102, Volume: 200}, // down: -200
	}

	result, err := uc.ComputeOBV("AAPL", bars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []float64{0, 500, 500, 300}
	if len(result) != len(want) {
		t.Fatalf("expected %d values, got %d", len(want), len(result))
	}
	for i, v := range want {
		if result[i] != v {
			t.Fatalf("index %d: expected OBV %v, got %v", i, v, result[i])
		}
	}
}

func TestComputeOBVHandlesEmptyBars(t *testing.T) {
	uc := NewUseCase()
	result, err := uc.ComputeOBV("AAPL", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result for empty bars, got %v", result)
	}
}

func TestComputeADXFallsBackWithInsufficientHistory(t *testing.T) {
	uc := NewUseCase()
	bars := make([]market.Bar, 20) // fewer than 2*adxPeriod (28)
	result, err := uc.ComputeADX("AAPL", bars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result for insufficient history, got %v", result)
	}
}

func TestComputeADXReflectsSustainedTrendStrength(t *testing.T) {
	uc := NewUseCase()
	bars := make([]market.Bar, 0, 40)
	price := 100.0
	for i := 0; i < 40; i++ {
		price += 2 // strong, steady uptrend: every bar makes a new high with no pullback
		bars = append(bars, market.Bar{
			Open: price - 1, High: price + 0.5, Low: price - 1.5, Close: price,
			Volume: 1000, Timestamp: time.Now().Add(time.Duration(i) * time.Hour),
		})
	}

	result, err := uc.ComputeADX("AAPL", bars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != len(bars) {
		t.Fatalf("expected %d values, got %d", len(bars), len(result))
	}
	last := result[len(result)-1]
	if last <= 20 {
		t.Fatalf("expected a strong, uninterrupted uptrend to produce a meaningfully elevated ADX, got %v", last)
	}
	if last < 0 || last > 100 {
		t.Fatalf("expected ADX to stay within [0,100], got %v", last)
	}
}

func TestComputeBollingerBandsRequiresFullWindow(t *testing.T) {
	uc := NewUseCase()
	bars := make([]market.Bar, 10) // fewer than bollingerPeriod (20)
	bands, err := uc.ComputeBollingerBands("AAPL", bars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bands.Middle) != 0 {
		t.Fatalf("expected an empty result before a full window, got %+v", bands)
	}
}

func TestComputeBollingerBandsOnConstantClosesCollapsesToZeroWidth(t *testing.T) {
	uc := NewUseCase()
	bars := make([]market.Bar, 20)
	for i := range bars {
		bars[i] = market.Bar{Close: 100, Timestamp: time.Now().Add(time.Duration(i) * time.Hour)}
	}

	bands, err := uc.ComputeBollingerBands("AAPL", bars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bands.Middle) != 1 {
		t.Fatalf("expected exactly one fully-formed window, got %d", len(bands.Middle))
	}
	if bands.Middle[0] != 100 || bands.Upper[0] != 100 || bands.Lower[0] != 100 {
		t.Fatalf("expected zero-width bands on constant closes, got upper=%v middle=%v lower=%v",
			bands.Upper[0], bands.Middle[0], bands.Lower[0])
	}
}

func TestComputeBollingerBandsWidensWithVolatility(t *testing.T) {
	uc := NewUseCase()
	bars := make([]market.Bar, 20)
	for i := range bars {
		close := 100.0
		if i%2 == 1 {
			close = 120.0 // alternating closes: real, non-trivial spread
		}
		bars[i] = market.Bar{Close: close, Timestamp: time.Now().Add(time.Duration(i) * time.Hour)}
	}

	bands, err := uc.ComputeBollingerBands("AAPL", bars)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bands.Upper[0] <= bands.Middle[0] || bands.Middle[0] <= bands.Lower[0] {
		t.Fatalf("expected upper > middle > lower for volatile closes, got upper=%v middle=%v lower=%v",
			bands.Upper[0], bands.Middle[0], bands.Lower[0])
	}
	if bands.Middle[0] != 110 {
		t.Fatalf("expected middle band (SMA) of 110 for closes alternating 100/120, got %v", bands.Middle[0])
	}
}
