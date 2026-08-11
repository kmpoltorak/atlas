package web

import (
	"fmt"
	"time"

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

// ErrorPage is the view model for templates/error.html — see RenderError.
type ErrorPage struct {
	Lang    Lang
	Status  int
	Message string
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
	// PriceAsOf maps a Position's Symbol to its most recent transaction's RFC3339 timestamp —
	// see priceAsOf (web_handlers.go). The same event that set Position.CurrentPrice, so this is
	// genuinely "as of when" that price is from, not a live quote timestamp.
	PriceAsOf map[string]string
	// AlertCounts is the watchlist's current non-HOLD recommendations, grouped by action (e.g.
	// {"BUY": 2, "SELL": 1}) — see alertCountsByAction. Nil when there are none (or the
	// watchlist is empty), matching the Alerts page's own "no active alerts" framing.
	AlertCounts map[string]int
}

// PortfolioPage is the view model for templates/portfolio.html.
type PortfolioPage struct {
	Lang         Lang
	Positions    []portfolio.Position
	Transactions []portfolio.Transaction
	// Rates — see DashboardPage.Rates.
	Rates map[string]float64
	// Currencies lists the currencies the record-transaction form's dropdown offers, e.g.
	// ["PLN", "EUR", "USD"] — see currency.All().
	Currencies []string
	// Symbols is the watchlist, offered as the record-transaction form's symbol dropdown so the
	// common case doesn't require typing a ticker by hand — see RecommendationsPage.Symbols.
	Symbols []string
	// PriceAsOf — see DashboardPage.PriceAsOf.
	PriceAsOf map[string]string
	Error     string
	Success   string
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
	// PollAt/PollError report the background alert-poll loop's last attempt (see
	// app.AlertPollStatus) — distinct from Warning above, which reflects only this page load's
	// own on-demand Evaluate() call. PollAt is the zero time when the loop hasn't run yet (e.g.
	// right after startup); PollError is empty when the last attempt succeeded.
	PollAt    time.Time
	PollError string
	// ActiveFor answers "how long has this alert been saying this?", keyed by symbol. A single
	// recommendation row can't: every row is stamped with the moment it was computed, so the
	// newest one always reads as "just now" no matter how long the action has actually held.
	// The value comes from walking that symbol's history back to where the action last changed
	// (persistence.CurrentActionStreaks). A symbol with no recorded history yet is simply absent,
	// and index on a missing key yields the zero ActiveDuration, which renders as blank.
	ActiveFor map[string]ActiveDuration
	// Info is per-symbol quote data (see SymbolInfo.LastPrice/PreviousClose/ChangePercent) for
	// the "Today" column — how the symbol is doing right now, independent of whether an hourly
	// bar has closed yet today (see providers.dropUnclosedBars). A symbol absent from this map,
	// or with HasQuote false, simply has no quote fetched yet — the column renders blank, not
	// the page failing.
	Info map[string]SymbolInfo
}

// ActiveDuration carries both renderings of the same span because the table needs both: Label is
// what a person reads, Seconds is what the column sorts on, since "2d 4h" and "45m" do not
// compare correctly as text.
type ActiveDuration struct {
	Label   string
	Seconds int64
}

// FormatActiveDuration renders how long ago since was, compactly enough for a table cell: "2d 4h",
// "3h 20m", "45m". Anything under a minute reads as "<1m" rather than "0m", which would claim
// more precision than the poll interval (five minutes) actually provides.
//
// A zero or future since returns the zero value — the first means no recorded history, the second
// means clock skew, and inventing a duration for either would be worse than showing nothing.
func FormatActiveDuration(lang Lang, since, now time.Time) ActiveDuration {
	if since.IsZero() || !since.Before(now) {
		return ActiveDuration{}
	}

	elapsed := now.Sub(since)
	days := int(elapsed.Hours()) / 24
	hours := int(elapsed.Hours()) % 24
	minutes := int(elapsed.Minutes()) % 60

	unitDay, unitHour, unitMinute := "d", "h", "m"
	if lang == LangPL {
		unitDay, unitHour, unitMinute = "d", "godz", "min"
	}

	var label string
	switch {
	case days > 0:
		label = fmt.Sprintf("%d%s %d%s", days, unitDay, hours, unitHour)
	case hours > 0:
		label = fmt.Sprintf("%d%s %d%s", hours, unitHour, minutes, unitMinute)
	case minutes > 0:
		label = fmt.Sprintf("%d%s", minutes, unitMinute)
	default:
		label = "<1" + unitMinute
	}

	return ActiveDuration{Label: label, Seconds: int64(elapsed.Seconds())}
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
	Lang  Lang
	Items []watchlist.Item
	// Info is what each symbol actually is (see market.InstrumentInfo), keyed by ticker, for
	// the Name/Type columns — a symbol absent from this map simply has none fetched yet (added
	// before this feature existed, or the "Refresh info" button hasn't run), and its row renders
	// those columns blank rather than the page failing.
	Info    map[string]SymbolInfo
	Error   string
	Success string
}

// SymbolInfo is one persistence.SymbolInfo row's rendering, shared by two pages that each read
// only the fields they show: the Watchlist page's Name/Type columns (WatchlistPage.Info — what
// a symbol is, fetched once) and the Alerts page's "Today" column (AlertsPage.Info — how it's
// doing right now, refreshed every alert-poll cycle). Deliberately not shown on Watchlist: a
// live, constantly-changing price doesn't belong on a page about which symbols are tracked, not
// how they're currently doing — see AlertsPage.Info's doc comment.
type SymbolInfo struct {
	Name string
	Type string
	// LastPrice/PreviousClose back Alerts' "Today" column — the live price vs. the prior
	// session's close (see internal/app/alerts_loop.go's refreshWatchlistQuotes), independent of
	// whether an hourly bar has closed yet today. Zero PreviousClose means no quote has been
	// fetched for this symbol yet (see HasQuote).
	LastPrice     float64
	PreviousClose float64
}

// HasQuote reports whether a live quote has been fetched for this symbol yet (see
// refreshWatchlistQuotes) — false right after a symbol is added, until the next poll cycle.
func (s SymbolInfo) HasQuote() bool {
	return s.PreviousClose > 0
}

// ChangePercent is today's move — LastPrice against the prior session's close — as a percentage
// (e.g. -2.94, not -0.0294). Meaningless when HasQuote is false; callers must check that first.
func (s SymbolInfo) ChangePercent() float64 {
	return (s.LastPrice/s.PreviousClose - 1) * 100
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

	// MLEnabled is the live decision.Orchestrator.MLEnabled() value — whether BUY/SELL/HOLD
	// scoring currently blends ML+Portfolio+Risk into the technical score, or scores from the
	// technical term alone. Seeded from ML_ENABLED at startup but flippable here without a
	// restart (see internal/app/app.go's resolveMLEnabled and migrations/015).
	MLEnabled bool

	// TrainerConfigured is true when TRAINER_URL is set (see config.Config.TrainerURL) — the
	// "Train new candidate" section always renders, but explains how to run
	// training/retrain.sh by hand instead when this is false, rather than showing a
	// button that can't actually do anything.
	TrainerConfigured bool
	TrainerStatus     TrainerStatus
	// TrainerError is set when TrainerConfigured but the trainer wasn't reachable when this
	// page was rendered (e.g. the atlas-trainer service isn't running) — shown instead of a
	// stale/zero TrainerStatus.
	TrainerError string

	// HasBackup is true once a backup has ever been taken (persistence.TimescaleDB.
	// LastBackupAt, migrations/017) — the "Flush all data" button stays disabled until it is,
	// so there's always a safety net before that button can do anything. LastBackupAt is the
	// zero time when HasBackup is false.
	HasBackup    bool
	LastBackupAt time.Time
}

// TrainerStatus mirrors trainer.Status (internal/infrastructure/trainer) for the Account page —
// kept as its own type so this package doesn't need to import the infrastructure layer.
type TrainerStatus struct {
	Running    bool
	StartedAt  string
	FinishedAt string
	ExitCode   int
	Output     string
	Failed     bool
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
