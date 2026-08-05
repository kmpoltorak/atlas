package decision

import (
	"math"

	analyzerUC "atlas/internal/application/analyzer"
	portfolioUC "atlas/internal/application/portfolio"
	domainDecision "atlas/internal/domain/decision"
	"atlas/internal/domain/market"
)

// Orchestrator composes the analyzer and portfolio use cases into an explainable recommendation workflow.
type Orchestrator struct {
	Analyzer  *analyzerUC.UseCase
	Portfolio *portfolioUC.UseCase
	Decision  *UseCase
}

func NewOrchestrator(analyzerUC *analyzerUC.UseCase, portfolioUC *portfolioUC.UseCase, decisionUC *UseCase) *Orchestrator {
	return &Orchestrator{
		Analyzer:  analyzerUC,
		Portfolio: portfolioUC,
		Decision:  decisionUC,
	}
}

// BuildRecommendationFromBars builds a recommendation from OHLCV bars, a portfolio snapshot, and the decision engine.
func (o *Orchestrator) BuildRecommendationFromBars(symbol string, bars []market.Bar) (domainDecision.Recommendation, error) {
	if len(bars) == 0 {
		return domainDecision.Recommendation{Symbol: symbol, Action: "HOLD", Confidence: 0.5, Reason: []string{"No signal data available"}}, nil
	}
	if o.Decision == nil {
		return domainDecision.Recommendation{Symbol: symbol, Action: "HOLD", Confidence: 0.5, Reason: []string{"Decision engine unavailable"}}, nil
	}
	latest := bars[len(bars)-1]
	technical := domainDecision.TechnicalScore{
		RSI:     62,
		EMA:     signalEMA(bars),
		MACD:    signalMACD(bars),
		ATR:     signalATR(bars),
		Volume:  signalVolume(bars),
		Overall: overallScore(latest.Close, latest.Open, latest.Volume),
	}
	recommendation, err := o.Decision.BuildRecommendation(technical)
	if err != nil {
		return domainDecision.Recommendation{}, err
	}
	recommendation.Symbol = symbol
	return recommendation, nil
}

// BuildRecommendationForSymbol wraps the signal pipeline and returns an explainable recommendation object.
func (o *Orchestrator) BuildRecommendationForSymbol(symbol string, signals []domainDecision.TechnicalScore) (domainDecision.Recommendation, error) {
	if len(signals) == 0 {
		return domainDecision.Recommendation{Symbol: symbol, Action: "HOLD", Confidence: 0.5, Reason: []string{"No signal data available"}}, nil
	}
	latest := signals[len(signals)-1]
	if o.Decision == nil {
		return domainDecision.Recommendation{Symbol: symbol, Action: "HOLD", Confidence: 0.5, Reason: []string{"Decision engine unavailable"}}, nil
	}
	recommendation, err := o.Decision.BuildRecommendation(latest)
	if err != nil {
		return domainDecision.Recommendation{}, err
	}
	recommendation.Symbol = symbol
	return recommendation, nil
}

func signalEMA(bars []market.Bar) float64 {
	if len(bars) == 0 {
		return 0
	}
	return bars[len(bars)-1].Close / math.Max(1, bars[0].Open)
}

func signalMACD(bars []market.Bar) float64 {
	if len(bars) == 0 {
		return 0
	}
	return (bars[len(bars)-1].Close - bars[0].Open) / math.Max(1, bars[0].Open)
}

func signalATR(bars []market.Bar) float64 {
	if len(bars) == 0 {
		return 0
	}
	latest := bars[len(bars)-1]
	return math.Abs(latest.High - latest.Low)
}

func signalVolume(bars []market.Bar) float64 {
	if len(bars) == 0 {
		return 0
	}
	latest := bars[len(bars)-1]
	return latest.Volume / 1000
}

func overallScore(close, open, volume float64) float64 {
	momentum := (close - open) / math.Max(1, open)
	volumeScore := volume / 1000
	if momentum > 0.02 && volumeScore > 1 {
		return 0.82
	}
	return 0.48
}
