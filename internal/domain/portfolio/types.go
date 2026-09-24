package portfolio

import "time"

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

	// PricedAt and PriceSource say where CurrentPrice came from and when, so every consumer —
	// the dashboard, the JSON API and the decision engine — is reading the same valuation with
	// the same caveats attached, instead of each one silently resolving the price its own way.
	//
	// This exists because they used to disagree. The dashboard overlaid a live quote; the
	// decision engine read the raw column, which only ever holds the price of the last recorded
	// trade. Buy at 100, watch it fall to 70, and the screen showed a loss while the engine
	// scored the position's risk as though it were still worth 100.
	PricedAt    time.Time   `json:"priced_at,omitzero"`
	PriceSource PriceSource `json:"price_source,omitempty"`
}

// PriceSource distinguishes a real market quote from the fallback. The distinction is the whole
// point: an absent quote must not be presented as a current price.
type PriceSource string

const (
	// PriceFromQuote: a market quote collected by the alert loop (see refreshWatchlistQuotes).
	PriceFromQuote PriceSource = "quote"
	// PriceFromTrade: no quote available, so CurrentPrice is the price of the position's last
	// recorded transaction. It is not a valuation — unrealized P&L computed against it is
	// structurally zero, which reads as "no loss" but means "no information".
	PriceFromTrade PriceSource = "trade"
)

// IsLive reports whether CurrentPrice is a market quote young enough to value the position with.
// maxAge is the caller's tolerance; markets close, so anything under a few days is normal.
func (p Position) IsLive(now time.Time, maxAge time.Duration) bool {
	return p.PriceSource == PriceFromQuote && !p.PricedAt.IsZero() && now.Sub(p.PricedAt) <= maxAge
}

// QuoteMaxAge is how old a quote may be before Atlas stops treating it as a current valuation.
// Four days, not one: a Friday quote is still the last traded price all through a long weekend,
// and calling that stale would flag every Monday morning as degraded.
const QuoteMaxAge = 4 * 24 * time.Hour

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

	// Timestamp is when the trade happened — the economic date, which the caller may backdate.
	Timestamp string `json:"timestamp"`

	// RecordedAt is when Atlas accepted this transaction, which is a different question from
	// when the trade happened and is what breaks ties between them.
	//
	// Two trades can share a Timestamp honestly: the dashboard form submits a past trade date with
	// no time, and two entries backdated to the same day are then identical. Replay has to order
	// them anyway, because BUY-then-SELL and SELL-then-BUY are not the same history — the second
	// is an oversell. Ordering by transaction id instead ordered them by a random UUID, so the
	// same two trades could replay either way, and after a reload the database's own row order
	// decided. RecordedAt is the acceptance order, persisted (migrations/021) so it survives that
	// reload.
	//
	// Empty on a transaction that predates the column or was built by a caller directly;
	// ordering then falls back to the id, which is at least stable.
	RecordedAt string `json:"recorded_at,omitempty"`
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
