package backtesting

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

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

// tradeCostRate is charged on each side of a trade, so a round trip costs twice this. Until it
// was added, every simulation traded for free and reported returns nobody could have earned —
// which matters most for exactly the strategies a backtest is likeliest to flatter, the ones that
// trade often for a small edge each time. 0.1% per side is a conservative retail default covering
// commission and spread; set it to the real brokerage fee before reading any result as money.
const tradeCostRate = 0.001

// slippageRate is the adverse price move assumed between the signal and the fill, charged on top
// of tradeCostRate. The two are separate because they are separate things: commission is a fee
// you can look up, slippage is what the market does to a marketable order — a gap open, a thin
// book, the spread you actually cross. Executing at the next bar's Open (see simulate) removes
// the impossible same-bar fill, but pretending the Open is obtainable to the tick is its own
// fiction, and a strategy that trades often is exactly the one this flatters.
//
// 0.05% per side is a placeholder for a liquid instrument, not a measurement. Calibrate it
// against real fills before reading any result as money; on an illiquid GPW small cap it is
// several times this.
const slippageRate = 0.0005

// buyFill and sellFill turn a bar's open into the price actually paid/received, moving it
// against the trade in both directions so a round trip is charged twice, once each way.
func buyFill(open float64) float64  { return open * (1 + slippageRate) }
func sellFill(open float64) float64 { return open * (1 - slippageRate) }

// fillPrice picks the right side of the spread for a reported trade.
func fillPrice(open float64, action string) float64 {
	if action == "SELL" {
		return sellFill(open)
	}
	return buyFill(open)
}

// UseCase runs a historical simulation of the real decision pipeline (analyzer + decision
// engine, via Orchestrator) against past bars. Per docs/backtesting.md's "No Future Data
// Leakage" principle, the decision at bar i sees only bars[:i] — bars that have closed — and
// executes at bar i's open. It used to see bars[:i+1] and fill at that same bar's close, which
// required knowing the closing price before placing the order.
type UseCase struct {
	Context      context.Context
	Orchestrator *decisionUC.Orchestrator
	// Admission bounds concurrent simulations (see admission.go). Nil means unbounded, which is
	// what tests and one-off callers want; the HTTP handlers always set it.
	Admission *Admission
}

// NewUseCase wires a backtesting use case with a bounded admission slot count, so a page reload
// or a script cannot put more CPU-bound simulations in flight than the container has cores —
// which would starve the alert loop sharing this process. Tests that want unbounded concurrency
// set Admission to nil explicitly.
func NewUseCase(orchestrator *decisionUC.Orchestrator) *UseCase {
	return &UseCase{
		Orchestrator: orchestrator,
		Admission:    NewAdmission(DefaultAdmissionSlots()),
	}
}

// Run simulates a single all-in/all-out strategy against the live decision engine's current
// thresholds (u.Orchestrator.Decision.Thresholds): BUY invests all available cash, SELL
// liquidates the entire position, HOLD does nothing. ML evaluation metrics (see evaluateML) and
// persistence (see internal/infrastructure/persistence's SaveBacktestRun, called from
// internal/app/handlers.go) are both real. See RunWalkForward for walk-forward
// train/validate/test splitting and threshold optimization (docs/backtesting.md's "Walk Forward
// Testing"/"Optimization" sections).
func (u *UseCase) Run(symbol string, bars []market.Bar, initialCapital float64, strategyName, strategyVersion string) (backtesting.StrategyResult, error) {
	admit, err := u.admit()
	if err != nil {
		return backtesting.StrategyResult{}, err
	}
	defer admit()

	frozen, release := u.freeze()
	defer release()
	return frozen.run(symbol, bars, initialCapital, strategyName, strategyVersion)
}

// admit waits for a simulation slot, bounded by this use case's context so a client that
// disconnects while queued stops holding a place in the queue.
func (u *UseCase) admit() (func(), error) {
	ctx := u.Context
	if ctx == nil {
		ctx = context.Background()
	}
	return u.Admission.Acquire(ctx)
}

func (u *UseCase) run(symbol string, bars []market.Bar, initialCapital float64, strategyName, strategyVersion string) (backtesting.StrategyResult, error) {
	if err := u.validateInputs(strategyName, initialCapital); err != nil {
		return backtesting.StrategyResult{}, err
	}
	if len(bars) <= minLookback {
		return backtesting.StrategyResult{}, errors.New("not enough historical bars to backtest")
	}
	result, err := u.simulate(symbol, bars, initialCapital, strategyName, strategyVersion, u.Orchestrator.Decision.Thresholds, u.Orchestrator.ML)
	if err != nil {
		return backtesting.StrategyResult{}, err
	}
	if u.Orchestrator.MLEnabled() {
		result.MLEvaluation = u.evaluateML(symbol, bars)
	}
	result.MLContribution, err = u.measureMLContribution(symbol, bars, initialCapital, strategyName, strategyVersion, result)
	if err != nil {
		return backtesting.StrategyResult{}, err
	}
	return result, nil
}

// measureMLContribution re-runs the identical simulation with the ML term removed and reports
// the difference, answering "does this model make money" rather than only "does it rank well" —
// see backtesting.MLContribution for why that distinction is the point.
//
// Returns nil (not a zero-valued struct) whenever there is no real trained model to attribute a
// difference to, matching evaluateML's guard: with only the deterministic stub loaded, both runs
// would be scoring the same made-up signal and the delta would be a fabricated number.
func (u *UseCase) measureMLContribution(symbol string, bars []market.Bar, initialCapital float64, strategyName, strategyVersion string, withML backtesting.StrategyResult) (*backtesting.MLContribution, error) {
	if !u.Orchestrator.MLEnabled() || u.Orchestrator.ML == nil || u.Orchestrator.ML.CurrentModel() == nil {
		return nil, nil
	}
	withoutML, err := u.simulate(symbol, bars, initialCapital, strategyName, strategyVersion, u.Orchestrator.Decision.Thresholds, nil)
	if err != nil {
		return nil, err
	}
	return &backtesting.MLContribution{
		WithMLTotalReturn:    withML.TotalReturn,
		WithMLSharpeRatio:    withML.SharpeRatio,
		WithMLMaxDrawdown:    withML.MaxDrawdown,
		WithMLTradeCount:     withML.TradeCount,
		WithMLWinRate:        withML.WinRate,
		WithoutMLTotalReturn: withoutML.TotalReturn,
		WithoutMLSharpeRatio: withoutML.SharpeRatio,
		WithoutMLMaxDrawdown: withoutML.MaxDrawdown,
		WithoutMLTradeCount:  withoutML.TradeCount,
		WithoutMLWinRate:     withoutML.WinRate,
		ReturnDelta:          withML.TotalReturn - withoutML.TotalReturn,
		SharpeDelta:          withML.SharpeRatio - withoutML.SharpeRatio,
		DrawdownDelta:        withML.MaxDrawdown - withoutML.MaxDrawdown,
	}, nil
}

// validateInputs checks the arguments every entry point (Run, RunWalkForward) shares, before
// either touches bars.
func (u *UseCase) validateInputs(strategyName string, initialCapital float64) error {
	if strategyName == "" {
		return errors.New("strategy name is required")
	}
	if initialCapital <= 0 || math.IsNaN(initialCapital) || math.IsInf(initialCapital, 0) {
		return errors.New("initial capital must be positive")
	}
	if u.Orchestrator == nil || u.Orchestrator.Decision == nil {
		return errors.New("orchestrator is not configured")
	}
	return nil
}

// simulate is the shared simulation core behind Run and RunWalkForward's train/validate/test
// splits: it runs the all-in/all-out strategy against an explicit Thresholds value rather than
// whatever the live decision engine currently holds, so the optimizer (see optimizeThresholds)
// can evaluate many candidates without ever touching u.Orchestrator.Decision itself. Caller must
// have already validated inputs and confirmed len(bars) > minLookback.
func (u *UseCase) simulate(symbol string, bars []market.Bar, initialCapital float64, strategyName, strategyVersion string, thresholds domainDecision.Thresholds, ml *mlUC.UseCase) (backtesting.StrategyResult, error) {
	cash := initialCapital
	quantity := 0.0
	entryPrice := 0.0
	wins, losses := 0, 0
	trades := make([]backtesting.Trade, 0)
	equityCurve := make([]float64, 0, len(bars)-minLookback+1)
	equityCurve = append(equityCurve, initialCapital)

	// The Orchestrator injected into UseCase is shared with live trading (see
	// internal/app/app.go) and its Portfolio reflects real, present-day positions — using it
	// directly here would leak live portfolio state into HasPosition (see
	// docs/decision_engine.md's ACCUMULATE/REDUCE/WAIT differentiation), making a historical
	// simulation depend on unrelated present-day state. simPortfolio instead starts empty and
	// is updated bar-by-bar below to mirror exactly the position this simulation itself has
	// built up, independent of whatever the live portfolio holds. Analyzer/ML are stateless with
	// respect to a specific portfolio or threshold set, so reusing them from the live
	// orchestrator is safe; Decision is rebuilt around the given thresholds specifically so
	// varying them here never mutates the live decision engine's own instance.
	simPortfolio := portfolioUC.NewUseCase()
	simDecision := decisionUC.NewUseCaseWithThresholds(thresholds)
	// ml is passed in rather than read off u.Orchestrator so Run can simulate the identical
	// strategy with the model removed — see MLContribution. A nil ml leaves the blend
	// technical-only, the same degradation path as running with no model configured at all.
	simOrchestrator := decisionUC.NewOrchestratorWithML(u.Orchestrator.Analyzer, simPortfolio, simDecision, ml)
	simOrchestrator.SetMLEnabled(u.Orchestrator.MLEnabled())
	simOrchestrator.SetPortfolioRiskEnabled(u.Orchestrator.PortfolioRiskEnabled())
	simOrchestrator.HistoricalClock = true
	if err := validateBars(bars); err != nil {
		return backtesting.StrategyResult{}, err
	}
	if simOrchestrator.MLEnabled() && ml != nil {
		if err := ml.ValidateEvaluationPeriod(bars[minLookback].Timestamp); err != nil {
			return backtesting.StrategyResult{}, err
		}
	}

	for i := minLookback; i < len(bars); i++ {
		if u.Context != nil {
			if err := u.Context.Err(); err != nil {
				return backtesting.StrategyResult{}, err
			}
		}
		window := bars[:i] // signal uses only previously closed bars; execute at this bar's open
		today := bars[i]

		if quantity > 0 {
			simPortfolio.Positions = []portfolio.Position{{
				Symbol: symbol, Quantity: quantity, AverageCost: entryPrice, CurrentPrice: bars[i-1].Close,
				// The simulation's own mark-to-market is a real valuation as of that bar, so it
				// must not read as a never-quoted position (see Orchestrator.HistoricalClock).
				PriceSource: portfolio.PriceFromQuote, PricedAt: bars[i-1].Timestamp,
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
				// entryPrice is the effective per-share cost basis including the entry fee, not
				// the market price — so the position's ROI (which reaches the decision engine via
				// simPortfolio.AverageCost) reflects what the shares actually cost.
				entryPrice = buyFill(today.Open) / (1 - tradeCostRate)
				quantity = cash / entryPrice
				cash = 0
				trades = append(trades, newTrade(today, symbol, recommendation, quantity))
			}
		case "SELL":
			if quantity > 0 {
				sold := quantity
				cash += quantity * sellFill(today.Open) * (1 - tradeCostRate)
				// A trade that gained less than the round trip cost is a loss, not a win.
				if sellFill(today.Open)*(1-tradeCostRate) > entryPrice {
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
	daily := dailyEquity(bars[minLookback:], equityCurve)
	volatility, sharpe := volatilityAndSharpe(daily, observationsPerYear(bars[minLookback:], len(daily)))

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
		MLEvaluation:    nil, // evaluated once after the strategy run, not for every optimizer candidate
	}, nil
}

// evaluateML compares the loaded ML model's correction-probability predictions against what
// actually happened afterward, per docs/backtesting.md's "ML Evaluation" section — a separate
// concern from the trading simulation above (it doesn't affect trades or equity), so it's kept
// as its own pass. Returns nil when no real trained model is loaded
// (u.Orchestrator.ML.CurrentModel() == nil, the deterministic RSI-derived stub): evaluating the
// stub's made-up correction-probability concept against reality would be a fabricated metric,
// not a real one.
func (u *UseCase) evaluateML(symbol string, bars []market.Bar) *backtesting.MLEvaluation {
	mlUseCase := u.Orchestrator.ML
	if mlUseCase == nil {
		return nil
	}
	model := mlUseCase.CurrentModel()
	if model == nil {
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
		prediction, err := mlUseCase.Predict(feature)
		probability := prediction.CorrectionProbability
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
		Timestamp: bar.Timestamp,
		Symbol:    symbol,
		Action:    recommendation.Action,
		// The fill, not the quoted open — a reported trade price the simulation didn't actually
		// use would make the trade list disagree with the equity curve derived from it.
		Price:      fillPrice(bar.Open, recommendation.Action),
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
// from a daily equity curve, scaled by periodsPerYear — how many observations this particular
// curve actually contains per year, measured from its own dates (see observationsPerYear).
// Hardcoding sqrt(252) was wrong twice over: it silently treated an hourly curve as daily, and
// it assumed every exchange runs the same number of sessions, which GPW does not.
func volatilityAndSharpe(equity []float64, periodsPerYear float64) (float64, float64) {
	if len(equity) < 2 || periodsPerYear <= 0 {
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

	scale := math.Sqrt(periodsPerYear)
	volatility := stddev * scale
	sharpe := 0.0
	if stddev > 0 {
		sharpe = (mean / stddev) * scale
	}
	return volatility, sharpe
}

// observationsPerYear is how many equity observations this curve holds per calendar year,
// measured from the first and last bar it was sampled from rather than assumed. A curve too
// short to measure (under a month) falls back to 252, since annualizing a two-week sample is
// meaningless either way and the fallback at least keeps the number comparable.
func observationsPerYear(bars []market.Bar, observations int) float64 {
	if len(bars) < 2 || observations < 2 {
		return 252
	}
	years := bars[len(bars)-1].Timestamp.Sub(bars[0].Timestamp).Hours() / 24 / 365.25
	if years < 1.0/12 {
		return 252
	}
	return float64(observations-1) / years
}

func annualize(totalReturn, days float64) float64 {
	years := days / 365
	if years <= 0 {
		return totalReturn
	}
	return math.Pow(1+totalReturn, 1/years) - 1
}

// dailyEquity uses the last observation of each UTC trading date. The initial capital
// remains the baseline, including entry costs in the first daily return.
func dailyEquity(bars []market.Bar, equity []float64) []float64 {
	if len(equity) == 0 {
		return nil
	}
	daily := []float64{equity[0]}
	for i, bar := range bars {
		if i+1 == len(bars) || bar.Timestamp.UTC().Format("2006-01-02") != bars[i+1].Timestamp.UTC().Format("2006-01-02") {
			daily = append(daily, equity[i+1])
		}
	}
	return daily
}

func validateBars(bars []market.Bar) error {
	for i, bar := range bars {
		if bar.Timestamp.IsZero() || (i > 0 && !bar.Timestamp.After(bars[i-1].Timestamp)) {
			return errors.New("bars must have strictly increasing timestamps")
		}
		for _, v := range []float64{bar.Open, bar.High, bar.Low, bar.Close} {
			if v <= 0 || math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("bar %s has invalid prices", bar.Timestamp.Format(time.RFC3339))
			}
		}
	}
	return nil
}

func (u *UseCase) WithContext(ctx context.Context) *UseCase {
	return &UseCase{Orchestrator: u.Orchestrator, Context: ctx, Admission: u.Admission}
}

func (u *UseCase) freeze() (*UseCase, func()) {
	if u.Orchestrator == nil {
		return u, func() {}
	}
	source := u.Orchestrator
	model := source.ML
	release := func() {}
	if model != nil {
		model, release = model.Snapshot()
	}
	frozen := decisionUC.NewOrchestratorWithML(source.Analyzer, nil, source.Decision, model)
	frozen.SetMLEnabled(source.MLEnabled())
	frozen.SetPortfolioRiskEnabled(source.PortfolioRiskEnabled())
	// No Admission on the frozen copy: the slot is already held by the caller, and re-acquiring
	// it here would deadlock a single-slot admission against itself.
	return &UseCase{Orchestrator: frozen, Context: u.Context}, release
}
