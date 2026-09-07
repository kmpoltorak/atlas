package collector

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"atlas/internal/domain/market"
)

type stubProvider struct {
	bars      []market.Bar
	err       error
	callCount *int
}

type stubCache struct {
	store map[string][]market.Bar
}

func (c *stubCache) key(symbol string, window int) string {
	return symbol + ":" + strconv.Itoa(window)
}

func (c *stubCache) Get(symbol string, window int) ([]market.Bar, bool) {
	bars, ok := c.store[c.key(symbol, window)]
	return bars, ok
}

func (c *stubCache) Set(symbol string, window int, bars []market.Bar) {
	if c.store == nil {
		c.store = map[string][]market.Bar{}
	}
	c.store[c.key(symbol, window)] = bars
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

func (s *stubStorage) GetBars(symbol string) ([]market.Bar, error) {
	return s.storedBars, s.err
}

func (s stubProvider) FetchInstrumentInfo(_ context.Context, symbol string) (market.InstrumentInfo, error) {
	return market.InstrumentInfo{Symbol: symbol}, nil
}

func (s stubProvider) FetchQuote(_ context.Context, symbol string) (market.Quote, error) {
	return market.Quote{Symbol: symbol}, nil
}

func (s stubProvider) FetchBars(_ context.Context, symbol string, window int) ([]market.Bar, error) {
	if s.callCount != nil {
		*s.callCount++
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.bars, nil
}

func TestCollectReturnsBarsWhenProviderConfigured(t *testing.T) {
	provider := stubProvider{bars: []market.Bar{{Symbol: "AAPL", Close: 100, Timestamp: time.Now()}}}
	uc := &UseCase{Provider: provider}

	bars, fromCache, err := uc.Collect(context.Background(), "AAPL", 1)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if fromCache {
		t.Fatal("expected the first collection to not be served from cache")
	}
	if len(bars) != 1 {
		t.Fatalf("expected one bar, got %d", len(bars))
	}
}

func TestCollectPersistsBarsWhenStorageEnabled(t *testing.T) {
	provider := stubProvider{bars: []market.Bar{{Symbol: "AAPL", Close: 100, Timestamp: time.Now()}}}
	storage := &stubStorage{}
	uc := &UseCase{Provider: provider, Storage: storage}

	bars, _, err := uc.Collect(context.Background(), "AAPL", 1)
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

func TestCollectServesFromCacheWithoutCallingProviderAgain(t *testing.T) {
	calls := 0
	provider := stubProvider{bars: []market.Bar{{Symbol: "AAPL", Close: 100, Timestamp: time.Now()}}, callCount: &calls}
	uc := &UseCase{Provider: provider, Cache: &stubCache{}}

	_, fromCache1, err := uc.Collect(context.Background(), "AAPL", 30)
	if err != nil {
		t.Fatalf("unexpected error on first collect: %v", err)
	}
	if fromCache1 {
		t.Fatal("expected the first collect to be a cache miss")
	}
	_, fromCache2, err := uc.Collect(context.Background(), "AAPL", 30)
	if err != nil {
		t.Fatalf("unexpected error on second collect: %v", err)
	}
	if !fromCache2 {
		t.Fatal("expected the second collect to be served from cache")
	}

	if calls != 1 {
		t.Fatalf("expected the provider to be called exactly once (second call served from cache), got %d", calls)
	}
}

func TestCollectRejectsMissingProviderOrSymbol(t *testing.T) {
	uc := &UseCase{}
	if _, _, err := uc.Collect(context.Background(), "AAPL", 1); err == nil {
		t.Fatal("expected provider error")
	}

	provider := stubProvider{err: errors.New("boom")}
	uc = &UseCase{Provider: provider}
	if _, _, err := uc.Collect(context.Background(), "", 1); err == nil {
		t.Fatal("expected symbol validation error")
	}
}
