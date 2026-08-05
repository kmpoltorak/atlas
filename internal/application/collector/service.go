package collector

import "atlas/internal/domain/market"

type Service interface {
	Collect(symbol string, window int) ([]market.Bar, error)
}
