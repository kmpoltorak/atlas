package analyzer

import (
	"math"

	"atlas/internal/domain/market"
)

// UseCase contains the first deterministic indicator calculations for the application layer.
type UseCase struct{}

func NewUseCase() *UseCase {
	return &UseCase{}
}

// ComputeRSI returns a simple 14-period Wilder RSI sequence.
func (u *UseCase) ComputeRSI(symbol string, bars []market.Bar) ([]float64, error) {
	if len(bars) < 15 {
		return nil, nil
	}
	closes := closeSeries(bars)
	gains := make([]float64, 0, len(closes)-1)
	losses := make([]float64, 0, len(closes)-1)
	for i := 1; i < len(closes); i++ {
		delta := closes[i] - closes[i-1]
		if delta >= 0 {
			gains = append(gains, delta)
			losses = append(losses, 0)
		} else {
			gains = append(gains, 0)
			losses = append(losses, -delta)
		}
	}
	avgGain := average(gains[:14])
	avgLoss := average(losses[:14])
	result := make([]float64, 0, len(closes))
	result = append(result, 50)
	for i := 14; i < len(closes); i++ {
		gain := gains[i-1]
		loss := losses[i-1]
		avgGain = (avgGain*13 + gain) / 14
		avgLoss = (avgLoss*13 + loss) / 14
		if avgLoss == 0 {
			result = append(result, 100)
			continue
		}
		RS := avgGain / avgLoss
		result = append(result, 100-(100/(1+RS)))
	}
	return result, nil
}

// ComputeEMA returns the exponential moving average sequence using a 14-period smoothing factor.
func (u *UseCase) ComputeEMA(symbol string, bars []market.Bar) ([]float64, error) {
	if len(bars) == 0 {
		return nil, nil
	}
	period := 14
	result := make([]float64, 0, len(bars))
	multiplier := 2.0 / float64(period+1)
	first := bars[0].Close
	result = append(result, first)
	for i := 1; i < len(bars); i++ {
		current := bars[i].Close
		prev := result[i-1]
		result = append(result, ((current-prev)*multiplier)+prev)
	}
	return result, nil
}

// ComputeMACD returns the MACD line calculated from 12 and 26-period EMAs.
func (u *UseCase) ComputeMACD(symbol string, bars []market.Bar) ([]float64, error) {
	if len(bars) == 0 {
		return nil, nil
	}
	ema12 := emaSeries(bars, 12)
	ema26 := emaSeries(bars, 26)
	result := make([]float64, len(bars))
	for i := range bars {
		result[i] = ema12[i] - ema26[i]
	}
	return result, nil
}

// ComputeATR returns the true range average over a 14-period window.
func (u *UseCase) ComputeATR(symbol string, bars []market.Bar) ([]float64, error) {
	if len(bars) < 2 {
		return nil, nil
	}
	result := make([]float64, 0, len(bars))
	for i := 0; i < len(bars); i++ {
		tr := 0.0
		if i == 0 {
			tr = bars[i].High - bars[i].Low
		} else {
			prev := bars[i-1]
			tr = math.Max(bars[i].High-bars[i].Low, math.Max(math.Abs(bars[i].High-prev.Close), math.Abs(bars[i].Low-prev.Close)))
		}
		result = append(result, tr)
	}
	return result, nil
}

// ComputeVolume returns normalized volume values relative to the rolling average.
func (u *UseCase) ComputeVolume(symbol string, bars []market.Bar) ([]float64, error) {
	if len(bars) == 0 {
		return nil, nil
	}
	result := make([]float64, len(bars))
	avgVol := averageVolume(bars)
	for i := range bars {
		if avgVol == 0 {
			result[i] = 0
			continue
		}
		result[i] = bars[i].Volume / avgVol
	}
	return result, nil
}

func average(values []float64) float64 {
	total := 0.0
	for _, value := range values {
		total += value
	}
	if len(values) == 0 {
		return 0
	}
	return total / float64(len(values))
}

func averageVolume(bars []market.Bar) float64 {
	total := 0.0
	for _, bar := range bars {
		total += bar.Volume
	}
	return total / float64(len(bars))
}

func closeSeries(bars []market.Bar) []float64 {
	result := make([]float64, len(bars))
	for i := range bars {
		result[i] = bars[i].Close
	}
	return result
}

func emaSeries(bars []market.Bar, period int) []float64 {
	result := make([]float64, len(bars))
	multiplier := 2.0 / float64(period+1)
	result[0] = bars[0].Close
	for i := 1; i < len(bars); i++ {
		result[i] = (bars[i].Close-result[i-1])*multiplier + result[i-1]
	}
	return result
}
