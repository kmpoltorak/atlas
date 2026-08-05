package domain

import "atlas/internal/domain/market"

type MarketRepository interface {
	SaveBars(symbol string, bars []market.Bar) error
}
