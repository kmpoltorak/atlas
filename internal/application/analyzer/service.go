package analyzer

import "atlas/internal/domain/market"

type Service interface {
	ComputeRSI(symbol string, bars []market.Bar) ([]float64, error)
	ComputeEMA(symbol string, bars []market.Bar) ([]float64, error)
	ComputeMACD(symbol string, bars []market.Bar) ([]float64, error)
	ComputeATR(symbol string, bars []market.Bar) ([]float64, error)
	ComputeVolume(symbol string, bars []market.Bar) ([]float64, error)
	ComputeOBV(symbol string, bars []market.Bar) ([]float64, error)
	ComputeADX(symbol string, bars []market.Bar) ([]float64, error)
	ComputeBollingerBands(symbol string, bars []market.Bar) (BollingerBands, error)
}
