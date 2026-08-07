package web

import (
	"atlas/internal/domain/alerts"
	backtestingDomain "atlas/internal/domain/backtesting"
	"atlas/internal/domain/decision"
	"atlas/internal/domain/portfolio"
	"atlas/internal/domain/watchlist"
)

// LoginPage is the view model for templates/login.html.
type LoginPage struct {
	Lang         Lang
	Error        string
	RequiresTOTP bool
}

// DashboardPage is the view model for templates/dashboard.html.
type DashboardPage struct {
	Lang      Lang
	Positions []portfolio.Position
	Risk      portfolio.RiskSnapshot
	// Rates is today's PLN-per-unit exchange rate for EUR/USD (keys "EUR"/"USD"; see
	// exchangerate.UseCase.Rates), used by the money template func to show every PLN-
	// denominated figure in all three currencies. Empty when NBP is unreachable and nothing is
	// cached yet — money degrades to PLN-only in that case, not an error.
	Rates map[string]float64
}

// PortfolioPage is the view model for templates/portfolio.html.
type PortfolioPage struct {
	Lang         Lang
	Positions    []portfolio.Position
	Transactions []portfolio.Transaction
	Risk         portfolio.RiskSnapshot
	// Rates — see DashboardPage.Rates.
	Rates map[string]float64
	// Currencies lists the currencies the record-transaction form's dropdown offers, e.g.
	// ["PLN", "EUR", "USD"] — see currency.All().
	Currencies []string
	Error      string
	Success    string
}

// RecommendationsPage is the view model for templates/recommendations.html. Symbols is the
// watchlist, offered as a dropdown so the common case doesn't require typing a ticker by hand —
// see recommendationsPageHandler.
type RecommendationsPage struct {
	Lang           Lang
	Symbol         string
	Symbols        []string
	Recommendation *decision.Recommendation
	Error          string
}

// AlertsPage is the view model for templates/alerts.html.
type AlertsPage struct {
	Lang    Lang
	Alerts  []alerts.Alert
	Warning string
}

// BacktestsPage is the view model for templates/backtests.html. Symbols is the watchlist,
// offered as a dropdown — see backtestsPageHandler.
type BacktestsPage struct {
	Lang    Lang
	Symbol  string
	Symbols []string
	Window  int
	Capital float64
	Result  *backtestingDomain.StrategyResult
	Error   string
}

// WatchlistPage is the view model for templates/watchlist.html.
type WatchlistPage struct {
	Lang    Lang
	Items   []watchlist.Item
	Error   string
	Success string
}

// AccountPage is the view model for templates/account.html.
type AccountPage struct {
	Lang        Lang
	Username    string
	TOTPEnabled bool
	Error       string
	Success     string
}

// TOTPEnrollPage is the view model for templates/totp_enroll.html.
type TOTPEnrollPage struct {
	Lang   Lang
	Secret string
	Error  string
}
