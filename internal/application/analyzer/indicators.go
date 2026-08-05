package analyzer

import "atlas/internal/domain/market"

type IndicatorCalculator interface {
	RSI(bars []market.Bar) ([]float64, error)
	EMA(bars []market.Bar) ([]float64, error)
	MACD(bars []market.Bar) ([]float64, error)
	ATR(bars []market.Bar) ([]float64, error)
	Volume(bars []market.Bar) ([]float64, error)
}

type Calculator struct{}

func NewCalculator() *Calculator {
	return &Calculator{}
}
