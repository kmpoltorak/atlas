// Package currency defines the currencies Atlas' portfolio recording understands and converts
// between. Scoped to exactly the three currencies the Author actually holds positions in — not a
// general ISO-4217 catalog.
package currency

// Code is a supported transaction/display currency.
type Code string

const (
	PLN Code = "PLN"
	EUR Code = "EUR"
	USD Code = "USD"
)

// Base is the currency portfolio math (weighted-average cost, exposure, etc.) is always done
// in internally — see portfolio.UseCase.applyTransaction. Every other currency is converted to
// and from Base via exchangerate.UseCase; the rest of the app (decision engine, risk, ROI,
// backtesting) never needs to know multi-currency exists at all.
const Base = PLN

// All lists every supported currency in a fixed, stable display order — used to populate
// dropdowns and to iterate a fixed set of columns when rendering a value in every currency.
func All() []Code {
	return []Code{PLN, EUR, USD}
}

// Valid reports whether c is one of the currencies Atlas supports.
func Valid(c Code) bool {
	return c == PLN || c == EUR || c == USD
}
