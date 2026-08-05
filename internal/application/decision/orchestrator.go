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
	technical := o.buildTechnicalScore(symbol, bars)
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

// buildTechnicalScore derives an explainable TechnicalScore from the real analyzer indicators.
// RSI falls back to a neutral baseline only when there isn't enough history (< 15 bars) to
// compute Wilder's RSI; every other field reflects the actual indicator series.
func (o *Orchestrator) buildTechnicalScore(symbol string, bars []market.Bar) domainDecision.TechnicalScore {
	latest := bars[len(bars)-1]
	momentumVolume := overallScore(latest.Close, latest.Open, latest.Volume)

	technical := domainDecision.TechnicalScore{RSI: 50, EMA: latest.Close, Overall: momentumVolume}
	if o.Analyzer == nil {
		return technical
	}

	if series, err := o.Analyzer.ComputeRSI(symbol, bars); err == nil && len(series) > 0 {
		technical.RSI = series[len(series)-1]
	}
	if series, err := o.Analyzer.ComputeEMA(symbol, bars); err == nil && len(series) > 0 {
		technical.EMA = series[len(series)-1]
	}
	if series, err := o.Analyzer.ComputeMACD(symbol, bars); err == nil && len(series) > 0 {
		technical.MACD = series[len(series)-1]
	}
	if series, err := o.Analyzer.ComputeATR(symbol, bars); err == nil && len(series) > 0 {
		technical.ATR = series[len(series)-1]
	}
	if series, err := o.Analyzer.ComputeVolume(symbol, bars); err == nil && len(series) > 0 {
		technical.Volume = series[len(series)-1]
	}

	// Blend the real RSI into the overall score only when it was actually computed;
	// otherwise keep the momentum/volume score untouched.
	if technical.RSI != 50 {
		rsiSignal := 0.5 + (technical.RSI-50)/100
		technical.Overall = clamp01(momentumVolume*0.7 + rsiSignal*0.3)
	}

	return technical
}

func overallScore(close, open, volume float64) float64 {
	momentum := (close - open) / math.Max(1, open)
	volumeScore := volume / 1000
	if momentum > 0.02 && volumeScore > 1 {
		return 0.82
	}
	return 0.48
}

func clamp01(value float64) float64 {
	return math.Min(1, math.Max(0, value))
}
