package collector

import (
	"errors"

	"atlas/internal/domain/market"
)

// UseCase implements the collector workflow for the first application milestone.
type UseCase struct {
	Provider market.MarketProvider
	Storage  Storage
	Cache    Cache
}

func NewUseCase(provider market.MarketProvider) *UseCase {
	return NewUseCaseWithStorage(provider, nil)
}

func NewUseCaseWithStorage(provider market.MarketProvider, storage Storage) *UseCase {
	return &UseCase{Provider: provider, Storage: storage}
}

func NewUseCaseWithStorageAndCache(provider market.MarketProvider, storage Storage, cache Cache) *UseCase {
	return &UseCase{Provider: provider, Storage: storage, Cache: cache}
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

// GetStoredBars returns previously collected bars for a symbol without calling the live provider.
func (u *UseCase) GetStoredBars(symbol string) ([]market.Bar, error) {
	if symbol == "" {
		return nil, errors.New("symbol is required")
	}
	if u.Storage == nil {
		return nil, errors.New("storage is not configured")
	}
	return u.Storage.GetBars(symbol)
}
