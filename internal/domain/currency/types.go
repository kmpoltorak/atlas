// Package currency defines the currencies Atlas' portfolio recording understands and converts
// between. Scoped to exactly the three currencies the Author actually holds positions in — not a
// general ISO-4217 catalog.
package currency

import "errors"

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

// ErrNoRateForDate means the rate source published nothing for the requested date — a weekend, a
// public holiday, or a date before its history starts. It is not a failure: callers walk back to
// the previous business day, which is the convention NBP itself documents.
//
// Declared here rather than in either the application port or the NBP adapter so neither has to
// import the other to recognise it.
var ErrNoRateForDate = errors.New("no exchange rate published for that date")
