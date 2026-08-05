package backtesting

import (
	"errors"
	"math"
	"strings"

	decisionUC "atlas/internal/application/decision"
	"atlas/internal/domain/backtesting"
	domainDecision "atlas/internal/domain/decision"
	"atlas/internal/domain/market"
)

// minLookback is the minimum number of bars visible before the first simulated decision, so
// early recommendations aren't computed against a near-empty window. It matches the RSI
// warm-up period used by the analyzer.
const minLookback = 15

// UseCase runs a historical simulation of the real decision pipeline (analyzer + decision
// engine, via Orchestrator) against past bars. Per docs/backtesting.md's "No Future Data
// Leakage" principle, the decision at bar i only ever sees bars[:i+1].
type UseCase struct {
	Orchestrator *decisionUC.Orchestrator
}

func NewUseCase(orchestrator *decisionUC.Orchestrator) *UseCase {
	return &UseCase{Orchestrator: orchestrator}
}

// Run simulates a single all-in/all-out strategy: BUY invests all available cash, SELL
// liquidates the entire position, HOLD does nothing. This is a first, honest increment of
// docs/backtesting.md's spec — walk-forward date-range splitting, ML evaluation metrics,
// parameter optimization, and persistence are not yet implemented.
func (u *UseCase) Run(symbol string, bars []market.Bar, initialCapital float64, strategyName, strategyVersion string) (backtesting.StrategyResult, error) {
	if strategyName == "" {
		return backtesting.StrategyResult{}, errors.New("strategy name is required")
	}
	if initialCapital <= 0 {
		return backtesting.StrategyResult{}, errors.New("initial capital must be positive")
	}
	if u.Orchestrator == nil {
		return backtesting.StrategyResult{}, errors.New("orchestrator is not configured")
	}
	if len(bars) <= minLookback {
		return backtesting.StrategyResult{}, errors.New("not enough historical bars to backtest")
	}

	cash := initialCapital
	quantity := 0.0
	entryPrice := 0.0
	wins, losses := 0, 0
	trades := make([]backtesting.Trade, 0)
	equityCurve := make([]float64, 0, len(bars)-minLookback)

	for i := minLookback; i < len(bars); i++ {
		window := bars[:i+1] // only data available at this bar's timestamp
		today := bars[i]

		recommendation, err := u.Orchestrator.BuildRecommendationFromBars(symbol, window)
		if err != nil {
			return backtesting.StrategyResult{}, err
		}

		switch recommendation.Action {
		case "BUY":
			if cash > 0 {
				entryPrice = today.Close
				quantity = cash / today.Close
				cash = 0
				trades = append(trades, newTrade(today, symbol, recommendation, quantity))
			}
		case "SELL":
			if quantity > 0 {
				sold := quantity
				cash += quantity * today.Close
				if today.Close > entryPrice {
					wins++
				} else {
					losses++
				}
				quantity = 0
				trades = append(trades, newTrade(today, symbol, recommendation, sold))
			}
		}

		equityCurve = append(equityCurve, cash+quantity*today.Close)
	}

	finalBar := bars[len(bars)-1]
	finalEquity := cash + quantity*finalBar.Close
	totalReturn := (finalEquity - initialCapital) / initialCapital
	volatility, sharpe := volatilityAndSharpe(equityCurve)

	winRate := 0.0
	if wins+losses > 0 {
		winRate = float64(wins) / float64(wins+losses)
	}

	return backtesting.StrategyResult{
		StrategyName:    strategyName,
		StrategyVersion: strategyVersion,
		StartedAt:       bars[minLookback].Timestamp,
		FinishedAt:      finalBar.Timestamp,
		TotalReturn:     totalReturn,
		AnnualReturn:    annualize(totalReturn, finalBar.Timestamp.Sub(bars[minLookback].Timestamp).Hours()/24),
		SharpeRatio:     sharpe,
		Volatility:      volatility,
		MaxDrawdown:     maxDrawdown(equityCurve),
		TradeCount:      len(trades),
		WinningTrades:   wins,
		LosingTrades:    losses,
		WinRate:         winRate,
		Trades:          trades,
	}, nil
}

func newTrade(bar market.Bar, symbol string, recommendation domainDecision.Recommendation, quantity float64) backtesting.Trade {
	return backtesting.Trade{
		Timestamp:  bar.Timestamp,
		Symbol:     symbol,
		Action:     recommendation.Action,
		Price:      bar.Close,
		Quantity:   quantity,
		Confidence: recommendation.Confidence,
		Reason:     strings.Join(recommendation.Reason, "; "),
	}
}

func maxDrawdown(equity []float64) float64 {
	if len(equity) == 0 {
		return 0
	}
	peak := equity[0]
	worst := 0.0
	for _, value := range equity {
		if value > peak {
			peak = value
		}
		if peak > 0 {
			if drawdown := (peak - value) / peak; drawdown > worst {
				worst = drawdown
			}
		}
	}
	return worst
}

// volatilityAndSharpe computes annualized volatility and Sharpe ratio (zero risk-free rate)
// from a daily equity curve.
func volatilityAndSharpe(equity []float64) (float64, float64) {
	if len(equity) < 2 {
		return 0, 0
	}
	returns := make([]float64, 0, len(equity)-1)
	for i := 1; i < len(equity); i++ {
		if equity[i-1] == 0 {
			continue
		}
		returns = append(returns, (equity[i]-equity[i-1])/equity[i-1])
	}
	if len(returns) == 0 {
		return 0, 0
	}

	mean := 0.0
	for _, r := range returns {
		mean += r
	}
	mean /= float64(len(returns))

	variance := 0.0
	for _, r := range returns {
		variance += (r - mean) * (r - mean)
	}
	variance /= float64(len(returns))
	stddev := math.Sqrt(variance)

	volatility := stddev * math.Sqrt(252)
	sharpe := 0.0
	if stddev > 0 {
		sharpe = (mean / stddev) * math.Sqrt(252)
	}
	return volatility, sharpe
}

func annualize(totalReturn, days float64) float64 {
	years := days / 365
	if years <= 0 {
		return totalReturn
	}
	return math.Pow(1+totalReturn, 1/years) - 1
}
