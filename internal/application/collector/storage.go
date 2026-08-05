package collector

import "atlas/internal/domain/market"

type Storage interface {
	SaveBars(symbol string, bars []market.Bar) error
	GetBars(symbol string) ([]market.Bar, error)
}
