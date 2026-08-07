package ml

import (
	analyzerUC "atlas/internal/application/analyzer"
	"atlas/internal/domain/market"
)

// Market condition regime labels — see ClassifyMarketCondition.
const (
	MarketConditionBull     = "BULL"
	MarketConditionBear     = "BEAR"
	MarketConditionSideways = "SIDEWAYS"
)

// adxTrendingThreshold follows the standard technical-analysis convention: ADX below this means
// price action lacks a clear directional trend (ranging/sideways), regardless of EMA direction.
const adxTrendingThreshold = 20.0

// ClassifyMarketCondition reports a symbol's trend regime (BULL/BEAR/SIDEWAYS) from ADX (trend
// strength) and EMA direction (trend direction). This is a deterministic technical-analysis
// rule, NOT a trained model — unlike CorrectionProbability/RecoveryProbability, market-regime
// classification via ADX+EMA is well-established technical analysis that doesn't need labeled
// historical outcomes to define, so it doesn't share their real-data-volume blocker (see
// docs/ml.md's Implementation status note). Real and usable today, regardless of how much
// history has been collected — bounded only by ADX's own ~28-bar warm-up requirement (see
// analyzer.ComputeADX). Returns "" when there isn't enough history to compute ADX/EMA at all.
func ClassifyMarketCondition(analyzer *analyzerUC.UseCase, symbol string, bars []market.Bar) string {
	if analyzer == nil {
		return ""
	}
	adxSeries, err := analyzer.ComputeADX(symbol, bars)
	if err != nil || len(adxSeries) == 0 {
		return ""
	}
	emaSeries, err := analyzer.ComputeEMA(symbol, bars)
	if err != nil || len(emaSeries) < 2 {
		return ""
	}

	if adxSeries[len(adxSeries)-1] < adxTrendingThreshold {
		return MarketConditionSideways
	}

	emaNow := emaSeries[len(emaSeries)-1]
	emaPrev := emaSeries[len(emaSeries)-2]
	if emaNow >= emaPrev {
		return MarketConditionBull
	}
	return MarketConditionBear
}
