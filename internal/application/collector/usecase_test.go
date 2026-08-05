package collector

import (
	"errors"
	"testing"
	"time"

	"atlas/internal/domain/market"
)

type stubProvider struct {
	bars []market.Bar
	err  error
}

type stubStorage struct {
	storedSymbol string
	storedBars   []market.Bar
	err          error
}

func (s *stubStorage) SaveBars(symbol string, bars []market.Bar) error {
	s.storedSymbol = symbol
	s.storedBars = append([]market.Bar(nil), bars...)
	return s.err
}

func (s stubProvider) FetchBars(symbol string, window int) ([]market.Bar, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.bars, nil
}

func TestCollectReturnsBarsWhenProviderConfigured(t *testing.T) {
	provider := stubProvider{bars: []market.Bar{{Symbol: "AAPL", Close: 100, Timestamp: time.Now()}}}
	uc := NewUseCase(provider)

	bars, err := uc.Collect("AAPL", 1)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(bars) != 1 {
		t.Fatalf("expected one bar, got %d", len(bars))
	}
}

func TestCollectPersistsBarsWhenStorageEnabled(t *testing.T) {
	provider := stubProvider{bars: []market.Bar{{Symbol: "AAPL", Close: 100, Timestamp: time.Now()}}}
	storage := &stubStorage{}
	uc := NewUseCaseWithStorage(provider, storage)

	bars, err := uc.Collect("AAPL", 1)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(bars) != 1 {
		t.Fatalf("expected one bar, got %d", len(bars))
	}
	if storage.storedSymbol != "AAPL" {
		t.Fatalf("expected storage symbol AAPL, got %q", storage.storedSymbol)
	}
	if len(storage.storedBars) != 1 {
		t.Fatalf("expected storage to receive one bar, got %d", len(storage.storedBars))
	}
}

func TestCollectRejectsMissingProviderOrSymbol(t *testing.T) {
	uc := NewUseCase(nil)
	if _, err := uc.Collect("AAPL", 1); err == nil {
		t.Fatal("expected provider error")
	}

	provider := stubProvider{err: errors.New("boom")}
	uc = NewUseCase(provider)
	if _, err := uc.Collect("", 1); err == nil {
		t.Fatal("expected symbol validation error")
	}
}
