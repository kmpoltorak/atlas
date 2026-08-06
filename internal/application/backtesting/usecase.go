package backtesting

import (
	"errors"
	"math"
	"sort"
	"strings"

	decisionUC "atlas/internal/application/decision"
	mlUC "atlas/internal/application/ml"
	portfolioUC "atlas/internal/application/portfolio"
	"atlas/internal/domain/backtesting"
	domainDecision "atlas/internal/domain/decision"
	"atlas/internal/domain/market"
	"atlas/internal/domain/portfolio"
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
// docs/backtesting.md's spec — walk-forward train/validate/test date-range splitting and
// parameter optimization are not yet implemented. ML evaluation metrics (see evaluateML) and
// persistence (see internal/infrastructure/persistence's SaveBacktestRun, called from
// internal/app/handlers.go) are.
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

	// The Orchestrator injected into UseCase is shared with live trading (see
	// internal/app/app.go) and its Portfolio reflects real, present-day positions — using it
	// directly here would leak live portfolio state into HasPosition (see
	// docs/decision_engine.md's ACCUMULATE/REDUCE/WAIT differentiation), making a historical
	// simulation depend on unrelated present-day state. simPortfolio instead starts empty and
	// is updated bar-by-bar below to mirror exactly the position this simulation itself has
	// built up, independent of whatever the live portfolio holds. Analyzer/Decision/ML are
	// stateless with respect to a specific portfolio, so reusing them from the live
	// orchestrator is safe.
	simPortfolio := portfolioUC.NewUseCase()
	simOrchestrator := decisionUC.NewOrchestratorWithML(u.Orchestrator.Analyzer, simPortfolio, u.Orchestrator.Decision, u.Orchestrator.ML)

	for i := minLookback; i < len(bars); i++ {
		window := bars[:i+1] // only data available at this bar's timestamp
		today := bars[i]

		if quantity > 0 {
			simPortfolio.Positions = []portfolio.Position{{
				Symbol: symbol, Quantity: quantity, AverageCost: entryPrice, CurrentPrice: today.Close,
			}}
		} else {
			simPortfolio.Positions = nil
		}

		recommendation, err := simOrchestrator.BuildRecommendationFromBars(symbol, window)
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
		Symbol:          symbol,
		InitialCapital:  initialCapital,
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
		MLEvaluation:    u.evaluateML(symbol, bars),
	}, nil
}

// evaluateML compares the loaded ML model's correction-probability predictions against what
// actually happened afterward, per docs/backtesting.md's "ML Evaluation" section — a separate
// concern from the trading simulation above (it doesn't affect trades or equity), so it's kept
// as its own pass. Returns nil when no real trained model is loaded (u.Orchestrator.ML.Model ==
// nil, the deterministic RSI-derived stub): evaluating the stub's made-up correction-probability
// concept against reality would be a fabricated metric, not a real one.
func (u *UseCase) evaluateML(symbol string, bars []market.Bar) *backtesting.MLEvaluation {
	model := u.Orchestrator.ML
	if model == nil || model.Model == nil {
		return nil
	}

	labelParams := mlUC.DefaultCorrectionLabelParams()
	var trueLabels []int
	var predictedProbs []float64

	for i := minLookback; i < len(bars); i++ {
		label, ok := mlUC.ComputeCorrectionLabel(bars, i, labelParams)
		if !ok {
			continue // not enough forward history yet to know the real outcome
		}
		feature := mlUC.BuildFeatureVector(u.Orchestrator.Analyzer, symbol, bars[:i+1])
		probability, err := model.Model.PredictCorrectionProbability(feature)
		if err != nil {
			continue
		}
		trueLabels = append(trueLabels, label)
		predictedProbs = append(predictedProbs, probability)
	}

	if len(trueLabels) == 0 {
		return nil
	}
	return computeMLEvaluation(trueLabels, predictedProbs)
}

// computeMLEvaluation scores correction-probability predictions (threshold 0.5 for the
// confusion-matrix metrics) against their real, forward-observed labels.
func computeMLEvaluation(trueLabels []int, predictedProbs []float64) *backtesting.MLEvaluation {
	tp, fp, tn, fn := 0, 0, 0, 0
	for i, label := range trueLabels {
		predicted := 0
		if predictedProbs[i] >= 0.5 {
			predicted = 1
		}
		switch {
		case label == 1 && predicted == 1:
			tp++
		case label == 0 && predicted == 1:
			fp++
		case label == 0 && predicted == 0:
			tn++
		default:
			fn++
		}
	}

	n := len(trueLabels)
	accuracy := float64(tp+tn) / float64(n)
	precision, recall, f1 := 0.0, 0.0, 0.0
	if tp+fp > 0 {
		precision = float64(tp) / float64(tp+fp)
	}
	if tp+fn > 0 {
		recall = float64(tp) / float64(tp+fn)
	}
	if precision+recall > 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}

	return &backtesting.MLEvaluation{
		SampleSize: n,
		Accuracy:   accuracy,
		Precision:  precision,
		Recall:     recall,
		F1Score:    f1,
		ROCAUC:     rocAUC(trueLabels, predictedProbs),
	}
}

// rocAUC computes the area under the ROC curve via the rank-sum (Mann-Whitney U) method:
// AUC = (sum of positive-class ranks - nPos*(nPos+1)/2) / (nPos*nNeg), with tied prediction
// scores sharing their average rank. This is exact and avoids picking an arbitrary set of
// thresholds to integrate over. Returns 0.5 (no discrimination either way) when one class is
// entirely absent from the sample, since AUC is undefined without both classes to rank against.
func rocAUC(trueLabels []int, predictedProbs []float64) float64 {
	n := len(trueLabels)
	nPos, nNeg := 0, 0
	for _, label := range trueLabels {
		if label == 1 {
			nPos++
		} else {
			nNeg++
		}
	}
	if nPos == 0 || nNeg == 0 {
		return 0.5
	}

	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return predictedProbs[order[a]] < predictedProbs[order[b]] })

	ranks := make([]float64, n)
	i := 0
	for i < n {
		j := i
		for j < n && predictedProbs[order[j]] == predictedProbs[order[i]] {
			j++
		}
		avgRank := float64(i+j+1) / 2.0 // 1-indexed average rank across the tied group [i,j)
		for k := i; k < j; k++ {
			ranks[order[k]] = avgRank
		}
		i = j
	}

	rankSumPositive := 0.0
	for i, label := range trueLabels {
		if label == 1 {
			rankSumPositive += ranks[i]
		}
	}

	u := rankSumPositive - float64(nPos)*float64(nPos+1)/2
	return u / (float64(nPos) * float64(nNeg))
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
