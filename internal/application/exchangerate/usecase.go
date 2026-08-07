// Package exchangerate converts portfolio amounts between currency.Base (PLN) and the other
// currencies Atlas supports (see internal/domain/currency), so a position built from mixed-
// currency transactions still has one mathematically coherent cost basis — see
// portfolio.UseCase.applyTransaction.
package exchangerate

import (
	"errors"
	"fmt"

	"atlas/internal/domain/currency"
)

// Provider fetches today's PLN-per-unit exchange rates for every non-PLN currency Atlas
// supports (see currency.All). A rate of 4.25 for EUR means 1 EUR = 4.25 PLN.
type Provider interface {
	FetchRates() (map[currency.Code]float64, error)
}

// Cache is a short-lived store in front of Provider so every page render doesn't call out to
// NBP — NBP itself only republishes once per business day (see docs/deployment.md), so caching
// bounds how often Atlas calls out, not how fresh the underlying data can possibly be. A cache
// miss or write failure degrades to calling Provider directly, never an error.
type Cache interface {
	Get() (map[currency.Code]float64, bool)
	Set(rates map[currency.Code]float64)
}

// UseCase serves the latest known PLN exchange rates and converts amounts to/from currency.Base.
type UseCase struct {
	Provider Provider
	Cache    Cache

	// lastKnown is an in-memory last-resort fallback beneath Cache (e.g. Redis itself being
	// briefly unreachable) — never persisted, just smooths over one bad request within a single
	// running process; a restart loses it, same as the rest of Atlas's caches.
	lastKnown map[currency.Code]float64
}

func NewUseCase(provider Provider, cache Cache) *UseCase {
	return &UseCase{Provider: provider, Cache: cache}
}

// Rates returns the latest known PLN-per-unit rate for every supported non-PLN currency,
// preferring a fresh Cache hit, then a live Provider fetch (refreshing Cache), then whatever was
// last successfully fetched in this process — in that order. It only errors when none of the
// three has ever produced a rate, since silently treating an unavailable rate as 1:1 would
// misrepresent real money.
func (u *UseCase) Rates() (map[currency.Code]float64, error) {
	if u.Cache != nil {
		if rates, ok := u.Cache.Get(); ok {
			return rates, nil
		}
	}
	if u.Provider != nil {
		if rates, err := u.Provider.FetchRates(); err == nil {
			u.lastKnown = rates
			if u.Cache != nil {
				u.Cache.Set(rates)
			}
			return rates, nil
		}
	}
	if u.lastKnown != nil {
		return u.lastKnown, nil
	}
	return nil, errors.New("no exchange rates available: NBP unreachable and nothing cached yet")
}

// ToBase converts amount in currency code to currency.Base (PLN). PLN itself always converts
// 1:1 without needing a live rate, so recording a plain PLN transaction never depends on NBP
// being reachable.
func (u *UseCase) ToBase(amount float64, code currency.Code) (float64, error) {
	if code == currency.Base {
		return amount, nil
	}
	rate, err := u.rateFor(code)
	if err != nil {
		return 0, err
	}
	return amount * rate, nil
}

// FromBase converts a currency.Base (PLN) amount into code — the inverse of ToBase, used to
// display an already-PLN figure (e.g. Position.AverageCost) in another currency.
func (u *UseCase) FromBase(amountBase float64, code currency.Code) (float64, error) {
	if code == currency.Base {
		return amountBase, nil
	}
	rate, err := u.rateFor(code)
	if err != nil {
		return 0, err
	}
	return amountBase / rate, nil
}

func (u *UseCase) rateFor(code currency.Code) (float64, error) {
	rates, err := u.Rates()
	if err != nil {
		return 0, err
	}
	rate, ok := rates[code]
	if !ok || rate <= 0 {
		return 0, fmt.Errorf("no exchange rate available for %s", code)
	}
	return rate, nil
}
