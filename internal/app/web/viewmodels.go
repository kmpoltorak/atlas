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

	// CorrectionModels/RecoveryModels list every trained model file ML_MODELS_DIR currently
	// has a metadata sidecar for (see ml.ListModels), newest first, for the model-picker
	// dropdowns below. Empty when the directory has nothing to offer yet — the section still
	// renders, just with no options, rather than being hidden outright.
	CorrectionModels []ModelOption
	RecoveryModels   []ModelOption
	// CorrectionModelActive/RecoveryModelActive is true when that role currently has a real
	// model loaded — used to decide whether to show a "none active" placeholder in the
	// dropdown instead of implying the first listed option is the active one.
	CorrectionModelActive bool
	RecoveryModelActive   bool
}

// ModelOption is one selectable entry in the Account page's model-activation dropdowns —
// Description is entirely derived from real training metadata (ml.ModelInfo.Description), never
// a fabricated or hand-edited string. Active marks whichever option (if any) is the model
// currently loaded for that role, pre-selected in the dropdown.
type ModelOption struct {
	Filename    string
	Description string
	Active      bool
}

// TOTPEnrollPage is the view model for templates/totp_enroll.html.
type TOTPEnrollPage struct {
	Lang   Lang
	Secret string
	Error  string
}
