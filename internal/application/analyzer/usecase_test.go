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
