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
	Error        string
	RequiresTOTP bool
}

// DashboardPage is the view model for templates/dashboard.html.
type DashboardPage struct {
	Positions []portfolio.Position
	Risk      portfolio.RiskSnapshot
}

// PortfolioPage is the view model for templates/portfolio.html.
type PortfolioPage struct {
	Positions    []portfolio.Position
	Transactions []portfolio.Transaction
	Risk         portfolio.RiskSnapshot
	Error        string
	Success      string
}

// RecommendationsPage is the view model for templates/recommendations.html. Symbols is the
// watchlist, offered as a dropdown so the common case doesn't require typing a ticker by hand —
// see recommendationsPageHandler.
type RecommendationsPage struct {
	Symbol         string
	Symbols        []string
	Recommendation *decision.Recommendation
	Error          string
}

// AlertsPage is the view model for templates/alerts.html.
type AlertsPage struct {
	Alerts  []alerts.Alert
	Warning string
}

// BacktestsPage is the view model for templates/backtests.html. Symbols is the watchlist,
// offered as a dropdown — see backtestsPageHandler.
type BacktestsPage struct {
	Symbol  string
	Symbols []string
	Window  int
	Capital float64
	Result  *backtestingDomain.StrategyResult
	Error   string
}

// WatchlistPage is the view model for templates/watchlist.html.
type WatchlistPage struct {
	Items   []watchlist.Item
	Error   string
	Success string
}

// AccountPage is the view model for templates/account.html.
type AccountPage struct {
	Username    string
	TOTPEnabled bool
	Error       string
	Success     string
}

// TOTPEnrollPage is the view model for templates/totp_enroll.html.
type TOTPEnrollPage struct {
	Secret string
	Error  string
}
