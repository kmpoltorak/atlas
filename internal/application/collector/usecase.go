package collector

import (
	"errors"

	"atlas/internal/domain/market"
)

// UseCase implements the collector workflow for the first application milestone.
type UseCase struct {
	Provider market.MarketProvider
	Storage  Storage
}

func NewUseCase(provider market.MarketProvider) *UseCase {
	return NewUseCaseWithStorage(provider, nil)
}

func NewUseCaseWithStorage(provider market.MarketProvider, storage Storage) *UseCase {
	return &UseCase{Provider: provider, Storage: storage}
}

// Collect retrieves bars from the configured provider and returns them to the caller.
func (u *UseCase) Collect(symbol string, window int) ([]market.Bar, error) {
	if u.Provider == nil {
		return nil, errors.New("provider is not configured")
	}
	if symbol == "" {
		return nil, errors.New("symbol is required")
	}

	bars, err := u.Provider.FetchBars(symbol, window)
	if err != nil {
		return nil, err
	}
	if u.Storage != nil {
		if err := u.Storage.SaveBars(symbol, bars); err != nil {
			return nil, err
		}
	}
	return bars, nil
}
