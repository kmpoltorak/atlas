package exchangerate

import (
	"errors"
	"testing"

	"atlas/internal/domain/currency"
)

type stubProvider struct {
	rates map[currency.Code]float64
	err   error
	calls int
}

func (p *stubProvider) FetchRates() (map[currency.Code]float64, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return p.rates, nil
}

type stubCache struct {
	rates map[currency.Code]float64
	found bool
	sets  int
}

func (c *stubCache) Get() (map[currency.Code]float64, bool) { return c.rates, c.found }
func (c *stubCache) Set(rates map[currency.Code]float64) {
	c.rates = rates
	c.found = true
	c.sets++
}

func TestRatesPrefersCacheHit(t *testing.T) {
	provider := &stubProvider{rates: map[currency.Code]float64{currency.EUR: 4.25, currency.USD: 3.73}}
	cache := &stubCache{rates: map[currency.Code]float64{currency.EUR: 4.99, currency.USD: 4.99}, found: true}
	uc := NewUseCase(provider, cache)

	rates, err := uc.Rates()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rates[currency.EUR] != 4.99 {
		t.Fatalf("expected the cached rate 4.99, got %v", rates[currency.EUR])
	}
	if provider.calls != 0 {
		t.Fatalf("expected the provider not to be called on a cache hit, got %d calls", provider.calls)
	}
}

func TestRatesFallsBackToProviderOnCacheMissAndRefreshesCache(t *testing.T) {
	provider := &stubProvider{rates: map[currency.Code]float64{currency.EUR: 4.25, currency.USD: 3.73}}
	cache := &stubCache{}
	uc := NewUseCase(provider, cache)

	rates, err := uc.Rates()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rates[currency.EUR] != 4.25 {
		t.Fatalf("expected the provider's rate, got %v", rates[currency.EUR])
	}
	if cache.sets != 1 {
		t.Fatalf("expected the cache to be refreshed once, got %d sets", cache.sets)
	}
}

func TestRatesFallsBackToLastKnownWhenProviderFailsAfterAnEarlierSuccess(t *testing.T) {
	provider := &stubProvider{rates: map[currency.Code]float64{currency.EUR: 4.25, currency.USD: 3.73}}
	uc := NewUseCase(provider, nil)

	if _, err := uc.Rates(); err != nil {
		t.Fatalf("unexpected error on first fetch: %v", err)
	}

	provider.err = errors.New("NBP unreachable")
	rates, err := uc.Rates()
	if err != nil {
		t.Fatalf("expected the last-known rate to be served despite the provider failing, got error: %v", err)
	}
	if rates[currency.EUR] != 4.25 {
		t.Fatalf("expected the last-known rate, got %v", rates[currency.EUR])
	}
}

func TestRatesErrorsWhenNothingHasEverSucceeded(t *testing.T) {
	provider := &stubProvider{err: errors.New("NBP unreachable")}
	uc := NewUseCase(provider, nil)

	if _, err := uc.Rates(); err == nil {
		t.Fatal("expected an error when the provider fails and nothing is cached")
	}
}

func TestToBasePLNIsAlwaysOneToOne(t *testing.T) {
	uc := NewUseCase(&stubProvider{err: errors.New("should never be called")}, nil)
	got, err := uc.ToBase(100, currency.PLN)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != 100 {
		t.Fatalf("expected PLN to convert 1:1, got %v", got)
	}
}

func TestToBaseAndFromBaseRoundTrip(t *testing.T) {
	provider := &stubProvider{rates: map[currency.Code]float64{currency.EUR: 4.0, currency.USD: 3.73}}
	uc := NewUseCase(provider, nil)

	pln, err := uc.ToBase(100, currency.EUR)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pln != 400 {
		t.Fatalf("expected 100 EUR to convert to 400 PLN at rate 4.0, got %v", pln)
	}

	eur, err := uc.FromBase(pln, currency.EUR)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if eur != 100 {
		t.Fatalf("expected the round trip to return exactly 100 EUR, got %v", eur)
	}
}

func TestToBaseErrorsForAnUnknownCurrency(t *testing.T) {
	provider := &stubProvider{rates: map[currency.Code]float64{currency.EUR: 4.0, currency.USD: 3.73}}
	uc := NewUseCase(provider, nil)
	if _, err := uc.ToBase(100, currency.Code("GBP")); err == nil {
		t.Fatal("expected an error for a currency with no known rate")
	}
}
