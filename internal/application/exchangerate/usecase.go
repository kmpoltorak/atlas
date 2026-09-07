// Package exchangerate converts portfolio amounts between currency.Base (PLN) and the other
// currencies Atlas supports (see internal/domain/currency), so a position built from mixed-
// currency transactions still has one mathematically coherent cost basis — see
// portfolio.UseCase.applyTransaction.
package exchangerate

import (
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"atlas/internal/domain/currency"
)

// Provider fetches today's PLN-per-unit exchange rates for every non-PLN currency Atlas
// supports (see currency.All). A rate of 4.25 for EUR means 1 EUR = 4.25 PLN.
type Provider interface {
	FetchRates() (map[currency.Code]float64, error)
}

// HistoricalProvider fetches the rate published on a specific past date. Separate from Provider
// because it is optional: a provider that can only answer "today" still works, callers just fall
// back to today's rate and are told they did.
type HistoricalProvider interface {
	FetchRateOn(code currency.Code, date time.Time) (float64, error)
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
	mu          sync.RWMutex
	lastKnownAt time.Time
	Provider    Provider
	Cache       Cache

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
			u.mu.Lock()
			u.lastKnown = maps.Clone(rates)
			u.lastKnownAt = time.Now()
			u.mu.Unlock()
			if u.Cache != nil {
				u.Cache.Set(rates)
			}
			return rates, nil
		}
	}
	u.mu.RLock()
	defer u.mu.RUnlock()
	if u.lastKnown != nil && time.Since(u.lastKnownAt) <= 24*time.Hour {
		return maps.Clone(u.lastKnown), nil
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

// maxRateLookback is how many days ToBaseOn walks backwards looking for a published rate. NBP
// publishes on business days only, so a trade dated on a Saturday has no rate of its own; the
// convention (and NBP's own guidance) is to use the last publication before it. Seven days covers
// a weekend plus the longest run of Polish public holidays.
const maxRateLookback = 7

// ToBaseOn converts amount into PLN using the rate NBP published on or before date, rather than
// today's rate.
//
// This matters for any transaction entered with a past timestamp — a backfilled trade, an import
// from a broker statement. Converting a March purchase at September's rate records a cost basis
// the trade never had, and every ROI, risk and recommendation derived from that position inherits
// the error silently.
//
// exact reports whether a dated rate was actually found. When it is false the caller got today's
// rate as a fallback and should say so rather than presenting the figure as historical — see
// portfolio.UseCase.RecordTransaction.
func (u *UseCase) ToBaseOn(amount float64, code currency.Code, date time.Time) (converted float64, exact bool, err error) {
	if code == currency.Base {
		return amount, true, nil
	}
	// A date in the future, today, or unset has no published historical rate to look up, so the
	// current rate *is* the right answer rather than a fallback — and a past date answered with
	// the current rate is a fallback however capable the provider is. exact tracks the date, not
	// the provider's capabilities: reporting it as !ok made the production NBP provider (which
	// does implement HistoricalProvider) return exact=false for every trade entered today, and
	// portfolio.convertToBase rejects those, so no same-day foreign-currency trade could be
	// recorded at all. The same inversion let a today-only provider price a backdated trade at
	// today's rate while claiming it was exact.
	current := date.IsZero() || !date.Before(time.Now().UTC().Truncate(24*time.Hour))
	historical, ok := u.Provider.(HistoricalProvider)
	if !ok || current {
		rate, err := u.rateFor(code)
		if err != nil {
			return 0, false, err
		}
		return amount * rate, current, nil
	}
	for day := 0; day <= maxRateLookback; day++ {
		rate, err := historical.FetchRateOn(code, date.AddDate(0, 0, -day))
		if err == nil && rate > 0 {
			return amount * rate, true, nil
		}
		if err != nil && !errors.Is(err, currency.ErrNoRateForDate) {
			break // a real failure, not a non-publication day: stop walking and fall back
		}
	}
	rate, err := u.rateFor(code)
	if err != nil {
		return 0, false, err
	}
	return amount * rate, false, nil
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
