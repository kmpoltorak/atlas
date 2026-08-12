package portfolio

// Portfolio is a named bucket of positions/transactions — e.g. "Main", "Safe", "Speculative".
// Every Atlas install starts with exactly one (auto-created on first boot); up to
// MaxPortfolios may exist at once (see application/portfolio.UseCase.CreatePortfolio).
type Portfolio struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Position's AverageCost/CurrentPrice are always denominated in currency.Base (PLN) — see
// UseCase.applyTransaction — regardless of what currency the underlying transactions were
// recorded in. Everything downstream (risk, ROI, decision-engine scoring, backtesting) reads
// these as plain PLN numbers and never needs to know multi-currency exists.
type Position struct {
	Symbol       string  `json:"symbol"`
	Quantity     float64 `json:"quantity"`
	AverageCost  float64 `json:"average_cost"`
	CurrentPrice float64 `json:"current_price"`
}

type Transaction struct {
	// ID is empty for a transaction not yet recorded (e.g. a caller-built value passed to
	// RecordTransaction); RecordTransaction assigns one before it's stored.
	ID       string  `json:"id"`
	Symbol   string  `json:"symbol"`
	Type     string  `json:"type"`
	Quantity float64 `json:"quantity"`

	// Price is in Currency, exactly as entered — the source of truth for display/audit.
	Price float64 `json:"price"`
	// Currency is validated against currency.Valid by RecordTransaction; defaults to
	// currency.Base (PLN) when empty, so existing callers that don't set it keep working
	// unchanged (the same non-fatal-optional stance the rest of this codebase takes).
	Currency string `json:"currency"`
	// PriceInPLN is Price converted to currency.Base at the moment this transaction was
	// recorded (via exchangerate.UseCase.ToBase), using whatever NBP rate was current then —
	// not recomputed later. This is what position math (UseCase.applyTransaction) actually
	// uses, so a position's weighted-average cost never silently drifts from FX movement alone;
	// it only changes when a real transaction is added or removed. Equal to Price when
	// Currency is PLN.
	PriceInPLN float64 `json:"price_in_pln"`

	Timestamp string `json:"timestamp"`
}

// RiskSnapshot is a cheap, single-portfolio-scan risk heuristic (see UseCase.ComputeRisk) — not
// the statistically real measures their field names suggest. Drawdown is unrealizedLoss/
// Exposure, not a peak-to-trough drawdown (there's no tracked equity-curve peak here); Volatility
// is the unweighted mean of |CurrentPrice-AverageCost| across positions, not a return-based
// standard deviation. Both are real, honestly-computed numbers — they are just a different,
// simpler statistic than their names imply. Contrast backtesting.Result's Volatility/MaxDrawdown
// (internal/domain/backtesting/types.go), which genuinely are annualized-stddev and
// peak-to-trough respectively — a different, unrelated pair of fields despite the shared names.
type RiskSnapshot struct {
	Exposure   float64 `json:"exposure"`
	Drawdown   float64 `json:"drawdown"`
	Volatility float64 `json:"volatility"`
}
