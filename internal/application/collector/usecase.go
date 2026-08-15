package collector

import (
	"errors"

	"atlas/internal/domain/market"
)

// UseCase fetches market data through a short-lived Redis cache, falling back to the live
// provider on a miss and persisting the result to TimescaleDB.
type UseCase struct {
	Provider market.MarketProvider
	Storage  Storage
	Cache    Cache
}

// Collect retrieves bars, preferring a cache hit over calling the live provider. fromCache
// tells the caller whether a live provider call actually happened, so callers that track
// provider-call metrics (rate-limit exposure, etc.) don't count cache hits as live requests.
func (u *UseCase) Collect(symbol string, window int) (bars []market.Bar, fromCache bool, err error) {
	if u.Provider == nil {
		return nil, false, errors.New("provider is not configured")
	}
	if symbol == "" {
		return nil, false, errors.New("symbol is required")
	}

	if u.Cache != nil {
		if cached, ok := u.Cache.Get(symbol, window); ok {
			return cached, true, nil
		}
	}

	bars, err = u.Provider.FetchBars(symbol, window)
	if err != nil {
		return nil, false, err
	}
	if u.Storage != nil {
		if err := u.Storage.SaveBars(symbol, bars); err != nil {
			return nil, false, err
		}
	}
	if u.Cache != nil {
		u.Cache.Set(symbol, window, bars)
	}
	return bars, false, nil
}
