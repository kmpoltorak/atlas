package app

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"atlas/internal/app/web"
	authUC "atlas/internal/application/auth"
	mlUC "atlas/internal/application/ml"
	portfolioUC "atlas/internal/application/portfolio"
	watchlistUC "atlas/internal/application/watchlist"
	"atlas/internal/domain/alerts"
	"atlas/internal/domain/currency"
	"atlas/internal/domain/portfolio"
	"atlas/internal/domain/watchlist"
	"atlas/internal/infrastructure/persistence"

	qrcode "github.com/skip2/go-qrcode"
	"go.uber.org/zap"
)

// exchangeRatesForDisplay returns today's PLN-per-unit rates as a plain map[string]float64 for
// templates (see web.money), or nil when unavailable (NBP unreachable, nothing cached yet) — the
// dashboard/portfolio pages must still render in that case, just without the EUR/USD secondary
// line, not fail outright.
func (a *Application) exchangeRatesForDisplay() map[string]float64 {
	if a.ExchangeRates == nil {
		return nil
	}
	rates, err := a.ExchangeRates.Rates()
	if err != nil {
		return nil
	}
	display := make(map[string]float64, len(rates))
	for code, rate := range rates {
		display[string(code)] = rate
	}
	return display
}

// renderError renders a page-handler failure as a styled HTML error page (see web.RenderError)
// instead of net/http's plain-text http.Error default, so a failure still looks like the rest
// of Atlas rather than a bare browser error page.
func (a *Application) renderError(w http.ResponseWriter, r *http.Request, status int, message string) {
	web.RenderError(w, a.resolveLang(r), status, message)
}

// currencyOptions lists the currencies the record-transaction form's dropdown offers, as plain
// strings for templates. Sorted alphabetically for the dropdown specifically — currency.All()'s
// own PLN-first order is kept as-is for its other callers (rate fetching, per-currency display
// columns), where "PLN first" reflects Base rather than being a dropdown listing.
func currencyOptions() []string {
	all := currency.All()
	options := make([]string, len(all))
	for i, code := range all {
		options[i] = string(code)
	}
	sort.Strings(options)
	return options
}

// loginPageHandler renders the login form (GET) and verifies credentials (POST) — see
// authUC.UseCase.Login. A GET while auth isn't Enabled() redirects straight to the dashboard,
// since there's nothing to log into. Whether the form should even show a TOTP field is read
// fresh from TOTPStore on every request (a.Auth.TOTPStatus()), not fixed at startup — 2FA can
// be enabled/disabled from the dashboard at any time.
func (a *Application) loginPageHandler(w http.ResponseWriter, r *http.Request) {
	lang := a.resolveLang(r)
	switch r.Method {
	case http.MethodGet:
		if !a.Auth.Enabled() {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		requiresTOTP, err := a.Auth.TOTPStatus()
		if err != nil {
			a.renderError(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		web.Render(w, "login", web.LoginPage{Lang: lang, RequiresTOTP: requiresTOTP})
	case http.MethodPost:
		requiresTOTP, statusErr := a.Auth.TOTPStatus()
		if err := r.ParseForm(); err != nil {
			web.Render(w, "login", web.LoginPage{Lang: lang, Error: "Invalid form submission.", RequiresTOTP: requiresTOTP})
			return
		}
		token, err := a.Auth.Login(r.Context(), r.FormValue("username"), r.FormValue("password"), r.FormValue("totp"))
		if err != nil {
			if statusErr != nil {
				a.renderError(w, r, http.StatusInternalServerError, statusErr.Error())
				return
			}
			message := "Invalid username, password, or verification code."
			if errors.Is(err, authUC.ErrTooManyAttempts) {
				message = "Too many failed login attempts. Wait a few minutes and try again."
			}
			web.Render(w, "login", web.LoginPage{Lang: lang, Error: message, RequiresTOTP: requiresTOTP})
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: sessionCookieName, Value: token, Path: "/",
			HttpOnly: true, Secure: a.Config.CookieSecure, SameSite: http.SameSiteLaxMode,
			MaxAge: int(authUC.SessionTTL.Seconds()),
		})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	default:
		methodNotAllowed(w)
	}
}

// logoutPageHandler invalidates the session server-side (not just clearing the cookie
// client-side) so a leaked/stolen cookie value stops working immediately, not just once it
// expires on its own.
func (a *Application) logoutPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		_ = a.Auth.DeleteSession(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: "", Path: "/", MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// dashboardPageHandler also doubles as "/"'s catch-all (see NewHandler's comment on the "/"
// registration — net/http.ServeMux treats it as a subtree pattern matching anything unmatched
// above it), so any path other than exactly "/" is a real 404, not the dashboard.
func (a *Application) dashboardPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		a.renderError(w, r, http.StatusNotFound, web.T(a.resolveLang(r), "error.not_found"))
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	positions, err := a.Portfolio.GetPositions()
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	transactions, err := a.Portfolio.GetTransactions()
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	livePositions, asOf := a.livePricedPositions(positions, priceAsOf(transactions))
	web.Render(w, "dashboard", web.DashboardPage{
		Lang: a.resolveLang(r), Positions: livePositions, Risk: portfolioUC.ComputeRiskSnapshot(livePositions),
		Rates:       a.exchangeRatesForDisplay(),
		PriceAsOf:   asOf,
		AlertCounts: a.alertCountsByAction(),
	})
}

// alertCountsByAction gives the dashboard's "Active Alerts" summary card a count per action
// (e.g. {"BUY": 2, "SELL": 1}), read from the actions the alert poll loop last recorded.
//
// It used to call Alerts.Evaluate, which meant every dashboard render collected bars for all 47
// watchlist symbols sequentially and ran the full orchestrator, ML inference included. With the
// 300s bars cache warm that cost about 0.18s; cold — after a restart, or once the TTL lapsed —
// it took 5 to 10 seconds. Measured over 24h of live traffic before this changed, "/" ran p50
// 0.183s but p90 8.4s, with 14 of 66 renders past 5s. That bimodality is exactly the "sometimes
// the dashboard hangs" the Author reported. The replacement is one indexed query, 7ms.
//
// The counts are now as of the last alert poll rather than computed on the spot. For a summary
// card that is the better number anyway: it agrees with the notification actually sent, instead
// of being a second, independently-computed opinion that could contradict it minutes later.
// Scoped to the current watchlist so a removed symbol's last recorded action stops being counted.
func (a *Application) alertCountsByAction() map[string]int {
	if a.Storage == nil {
		return nil
	}
	streaks, err := a.Storage.CurrentActionStreaks()
	if err != nil {
		a.Logger.Warn("dashboard alert summary: could not read recorded actions", zap.Error(err))
		return nil
	}
	items, err := a.Watchlist.List()
	if err != nil {
		a.Logger.Warn("dashboard alert summary: could not read the watchlist", zap.Error(err))
		return nil
	}

	return countActionable(streaks, items)
}

// countActionable tallies each watchlist symbol's currently-recorded action, skipping HOLD.
// Split out from alertCountsByAction purely so the two rules that can silently put a wrong
// number on screen — skip HOLD, and count only symbols still on the watchlist — are testable
// without a database behind them.
func countActionable(streaks map[string]persistence.ActionStreak, items []watchlist.Item) map[string]int {
	counts := make(map[string]int)
	for _, item := range items {
		if streak, ok := streaks[item.Symbol]; ok && streak.Action != "HOLD" {
			counts[streak.Action]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	return counts
}

// portfolioPageHandler lists positions (GET) and records a transaction (POST) — the dashboard
// equivalent of GET/POST /portfolio, and currently the only reason Phase 8 exists at all (see
// implementation-plan.md): recording BUY/SELL without hand-crafting an HTTP request.
func (a *Application) portfolioPageHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.renderPortfolioPage(w, r, "", "")
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			a.renderPortfolioPage(w, r, "Invalid form submission.", "")
			return
		}
		quantity, qErr := strconv.ParseFloat(r.FormValue("quantity"), 64)
		price, pErr := strconv.ParseFloat(r.FormValue("price"), 64)
		if qErr != nil || pErr != nil {
			a.renderPortfolioPage(w, r, "Quantity and price must both be numbers.", "")
			return
		}
		tx := portfolio.Transaction{
			Symbol:   strings.ToUpper(strings.TrimSpace(r.FormValue("symbol"))),
			Type:     r.FormValue("type"),
			Quantity: quantity,
			Price:    price,
			Currency: r.FormValue("currency"),
		}
		if err := a.Portfolio.RecordTransaction(tx); err != nil {
			a.renderPortfolioPage(w, r, err.Error(), "")
			return
		}
		go a.refreshRecommendation(tx.Symbol)
		a.renderPortfolioPage(w, r, "", "Transaction recorded.")
	default:
		methodNotAllowed(w)
	}
}

// portfolioDeleteHandler removes a position entirely (e.g. a test entry recorded by mistake) —
// the dashboard equivalent of DELETE /portfolio. Distinct from recording a SELL: this clears
// the current position aggregate without adding a transaction record (see
// portfolio.UseCase.RemovePosition).
func (a *Application) portfolioDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderPortfolioPage(w, r, "Invalid form submission.", "")
		return
	}
	symbol := strings.ToUpper(strings.TrimSpace(r.FormValue("symbol")))
	if err := a.Portfolio.RemovePosition(symbol); err != nil {
		a.renderPortfolioPage(w, r, err.Error(), "")
		return
	}
	go a.refreshRecommendation(symbol)
	a.renderPortfolioPage(w, r, "", "Removed "+symbol+" from your positions.")
}

// portfolioBulkDeleteHandler removes several positions at once — the Positions table's
// checkbox-select "Delete selected" action (see portfolio.UseCase.RemovePositions). Reads
// r.Form["symbols"] rather than a single FormValue, one per checked checkbox, distinct from the
// per-row Remove buttons in the same template, which still submit a single "symbol" to
// portfolioDeleteHandler via their own formaction.
func (a *Application) portfolioBulkDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderPortfolioPage(w, r, "Invalid form submission.", "")
		return
	}
	symbols := r.Form["symbols"]
	if len(symbols) == 0 {
		a.renderPortfolioPage(w, r, "No positions selected.", "")
		return
	}
	if err := a.Portfolio.RemovePositions(symbols); err != nil {
		a.renderPortfolioPage(w, r, err.Error(), "")
		return
	}
	a.renderPortfolioPage(w, r, "", fmt.Sprintf("%d position(s) removed.", len(symbols)))
}

// portfolioTransactionDeleteHandler removes one historical transaction by id (see
// portfolio.UseCase.RemoveTransaction) — the dashboard equivalent of DELETE /transactions.
// Distinct from portfolioDeleteHandler: this touches the transaction history itself and
// recomputes the affected position from what remains, rather than just clearing the current
// aggregate.
func (a *Application) portfolioTransactionDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderPortfolioPage(w, r, "Invalid form submission.", "")
		return
	}
	id := r.FormValue("id")
	symbol := a.transactionSymbol(id)
	if err := a.Portfolio.RemoveTransaction(id); err != nil {
		a.renderPortfolioPage(w, r, err.Error(), "")
		return
	}
	if symbol != "" {
		go a.refreshRecommendation(symbol)
	}
	a.renderPortfolioPage(w, r, "", "Transaction removed and position recomputed.")
}

// portfolioTransactionBulkDeleteHandler removes several historical transactions at once — the
// dashboard's checkbox-select "Delete selected" action (see portfolio.UseCase.RemoveTransactions).
// Reads r.Form["ids"] rather than a single FormValue: the bulk-delete form submits one "ids"
// field per checked checkbox (standard repeated-name form encoding), distinct from the per-row
// delete buttons in the same template, which still submit a single "id" to
// portfolioTransactionDeleteHandler via their own formaction.
func (a *Application) portfolioTransactionBulkDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderPortfolioPage(w, r, "Invalid form submission.", "")
		return
	}
	ids := r.Form["ids"]
	if len(ids) == 0 {
		a.renderPortfolioPage(w, r, "No transactions selected.", "")
		return
	}
	if err := a.Portfolio.RemoveTransactions(ids); err != nil {
		a.renderPortfolioPage(w, r, err.Error(), "")
		return
	}
	a.renderPortfolioPage(w, r, "", fmt.Sprintf("%d transaction(s) removed and positions recomputed.", len(ids)))
}

func (a *Application) renderPortfolioPage(w http.ResponseWriter, r *http.Request, errMsg, successMsg string) {
	positions, err := a.Portfolio.GetPositions()
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	transactions, err := a.Portfolio.GetTransactions()
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	livePositions, asOf := a.livePricedPositions(positions, priceAsOf(transactions))
	data := web.PortfolioPage{
		Lang: a.resolveLang(r), Positions: livePositions, Transactions: transactions,
		Rates: a.exchangeRatesForDisplay(), Currencies: currencyOptions(),
		PriceAsOf: asOf,
		Error:     errMsg, Success: successMsg,
	}
	if symbols, err := a.watchlistSymbols(); err == nil {
		data.Symbols = symbols
	}
	web.Render(w, "portfolio", data)
}

// livePricedPositions overlays each position's live quote (see refreshWatchlistQuotes) onto
// CurrentPrice and asOf, so the P&L/risk shown on Portfolio/Dashboard reflect the current market
// instead of each symbol's last recorded trade (see portfolio.applyTransaction's own doc comment
// for why CurrentPrice is otherwise frozen at trade time). asOf keeps its transaction-time
// fallback for a symbol with no live quote yet — never held while on the watchlist, or no poll
// cycle since being bought. Best-effort: a failed quote lookup leaves every position at its
// existing price, matching this page's existing degrade-gracefully stance elsewhere.
func (a *Application) livePricedPositions(positions []portfolio.Position, asOf map[string]string) ([]portfolio.Position, map[string]string) {
	if a.Storage == nil {
		return positions, asOf
	}
	quotes, err := a.Storage.SymbolInfoByTicker()
	if err != nil {
		a.Logger.Warn("failed to load live quotes for portfolio pricing", zap.Error(err))
		return positions, asOf
	}
	return overlayLivePrices(positions, asOf, quotes)
}

// overlayLivePrices does livePricedPositions' actual overlay — split out so it's testable
// without a database behind it, same reasoning as countActionable/filterCurrentAlerts above.
func overlayLivePrices(positions []portfolio.Position, asOf map[string]string, quotes map[string]persistence.SymbolInfo) ([]portfolio.Position, map[string]string) {
	priced := make([]portfolio.Position, len(positions))
	live := make(map[string]string, len(asOf))
	for symbol, ts := range asOf {
		live[symbol] = ts
	}
	for i, position := range positions {
		priced[i] = position
		if quote, ok := quotes[position.Symbol]; ok && quote.LastPrice > 0 {
			priced[i].CurrentPrice = quote.LastPrice
			live[position.Symbol] = quote.QuoteUpdatedAt.Format(time.RFC3339)
		}
	}
	return priced, live
}

// priceAsOf maps each symbol to its most recent transaction's timestamp — the same event that
// last set Position.CurrentPrice (see portfolio.applyTransaction: every BUY/SELL sets
// CurrentPrice = tx.PriceInPLN), so this is genuinely "as of when" the displayed price is from.
// Plain string comparison is enough to find the max: every Timestamp is written as
// time.Now().UTC().Format(time.RFC3339) (see web_handlers.go/alerts_loop.go), which is
// lexicographically sortable for a fixed UTC "Z"-suffixed format.
func priceAsOf(transactions []portfolio.Transaction) map[string]string {
	latest := make(map[string]string)
	for _, tx := range transactions {
		if current, ok := latest[tx.Symbol]; !ok || tx.Timestamp > current {
			latest[tx.Symbol] = tx.Timestamp
		}
	}
	return latest
}

// watchlistSymbols returns the watchlist as a plain []string, alphabetically sorted — used to
// populate the recommendations/backtests page dropdowns so the common case doesn't require
// typing a ticker by hand. Sorted here for the dropdown only; the Watchlist page's own table
// keeps a.Watchlist.List()'s natural (added) order.
func (a *Application) watchlistSymbols() ([]string, error) {
	items, err := a.Watchlist.List()
	if err != nil {
		return nil, err
	}
	symbols := make([]string, len(items))
	for i, item := range items {
		symbols[i] = item.Symbol
	}
	sort.Strings(symbols)
	return symbols, nil
}

// recommendationsPageHandler mirrors recommendationsHandler (GET /recommendations) — same
// orchestrator call, same persistence side effects (recommendation + indicator snapshot) — just
// rendered as HTML instead of JSON.
func (a *Application) recommendationsPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	symbol := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))
	data := web.RecommendationsPage{Lang: a.resolveLang(r), Symbol: symbol}
	if symbols, err := a.watchlistSymbols(); err == nil {
		data.Symbols = symbols
	}
	if symbol != "" {
		bars, err := a.collectBars(symbol, a.windowFromQuery(r))
		if err != nil {
			data.Error = err.Error()
		} else if rec, err := a.Orchestrator.BuildRecommendationFromBars(symbol, bars); err != nil {
			data.Error = err.Error()
		} else {
			a.Metrics.RecommendationsTotal.WithLabelValues(rec.Action).Inc()
			a.persistRecommendation(rec)
			a.persistIndicatorSnapshot(symbol, bars)
			data.Recommendation = &rec
		}
	}
	web.Render(w, "recommendations", data)
}

// alertsPageHandler mirrors alertsHandler (GET /alerts) — same data source (currentAlerts).
func (a *Application) alertsPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	result, err := a.currentAlerts()
	lang := a.resolveLang(r)
	data := web.AlertsPage{Lang: lang, Alerts: result, ActiveFor: a.activeAlertDurations(lang, result), Info: a.loadSymbolInfo()}
	if err != nil {
		data.Warning = err.Error()
	}
	data.PollAt, data.PollError = a.AlertPoll.Snapshot()
	web.Render(w, "alerts", data)
}

// activeAlertDurations reports, per alerted symbol, how long it has held its current recommended
// action — the one thing the alerts themselves cannot say, since every recommendation is stamped
// with the moment it was computed rather than the moment the signal appeared.
//
// The streak is only reported when the stored action still matches the action currentAlerts just
// read. Both now come from the same recommendations table (currentAlerts's LatestRecommendations,
// this function's CurrentActionStreaks), so the two disagreeing takes a poll writing between the
// two queries — rare, but the guard costs nothing and a wrong duration next to the wrong action
// would be worse than a blank cell.
//
// Best-effort by design: no storage, or a failed query, costs the duration column and nothing
// else. Alerts are the point of the page and must still render.
func (a *Application) activeAlertDurations(lang web.Lang, result []alerts.Alert) map[string]web.ActiveDuration {
	if a.Storage == nil || len(result) == 0 {
		return nil
	}
	streaks, err := a.Storage.CurrentActionStreaks()
	if err != nil {
		a.Logger.Warn("failed to load recommendation streaks for the alerts page", zap.Error(err))
		return nil
	}

	now := time.Now().UTC()
	durations := make(map[string]web.ActiveDuration, len(result))
	for _, alert := range result {
		streak, found := streaks[alert.Recommendation.Symbol]
		if !found || streak.Action != alert.Recommendation.Action {
			continue
		}
		durations[alert.Recommendation.Symbol] = web.FormatActiveDuration(lang, streak.Since.UTC(), now)
	}
	return durations
}

// backtestsPageHandler mirrors backtestsHandler (GET /backtests) — same simulation, same
// persistence, fixed to the "atlas-orchestrator"/"v1" strategy tag (the dashboard form has no
// use for the strategy-name/version query params the JSON API exposes for scripting).
func (a *Application) backtestsPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	symbol := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("symbol")))
	window := backtestDefaultWindow
	if raw := r.URL.Query().Get("window"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			window = parsed
		}
	}
	capital := backtestDefaultCapital
	if raw := r.URL.Query().Get("capital"); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil && parsed > 0 {
			capital = parsed
		}
	}

	data := web.BacktestsPage{Lang: a.resolveLang(r), Symbol: symbol, Window: window, Capital: capital}
	if symbols, err := a.watchlistSymbols(); err == nil {
		data.Symbols = symbols
	}
	if symbol != "" {
		bars, err := a.collectBars(symbol, window)
		if err != nil {
			data.Error = err.Error()
		} else if result, err := a.Backtesting.Run(symbol, bars, capital, "atlas-orchestrator", "v1"); err != nil {
			data.Error = err.Error()
		} else {
			a.persistBacktestRun(result)
			data.Result = &result
		}
	}
	web.Render(w, "backtests", data)
}

// watchlistPageHandler lists watched symbols (GET) and adds one (POST) — the dashboard
// equivalent of GET/POST /watchlist.
func (a *Application) watchlistPageHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.renderWatchlistPage(w, r, "", "")
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			a.renderWatchlistPage(w, r, "Invalid form submission.", "")
			return
		}
		symbol := strings.ToUpper(strings.TrimSpace(r.FormValue("symbol")))
		if err := a.Watchlist.Add(symbol, time.Now().UTC().Format(time.RFC3339)); err != nil {
			a.renderWatchlistPage(w, r, err.Error(), "")
			return
		}
		if err := a.fetchAndSaveSymbolInfo(symbol); err != nil {
			a.Logger.Warn("failed to fetch symbol info for newly added watchlist item", zap.String("symbol", symbol), zap.Error(err))
		}
		a.renderWatchlistPage(w, r, "", "Added "+symbol+" to the watchlist.")
	default:
		methodNotAllowed(w)
	}
}

// fetchAndSaveSymbolInfo best-effort fetches and persists what a symbol actually is (see
// market.InstrumentInfo) — its display name and instrument type (EQUITY/ETF/FUTURE/...), read
// off the market provider and shown on the Watchlist page so a ticker doesn't have to be looked
// up elsewhere. Called right after a symbol is added (see watchlistPageHandler) and by
// watchlistRefreshInfoHandler for symbols added before this existed. Best-effort: the watchlist
// item itself is already saved by the time this runs, so a lookup failure here should leave that
// row's Name/Type blank, not fail the request — same non-fatal stance as persistIndicatorSnapshot.
func (a *Application) fetchAndSaveSymbolInfo(symbol string) error {
	if a.Collector == nil || a.Collector.Provider == nil || a.Storage == nil {
		return nil
	}
	info, err := a.Collector.Provider.FetchInstrumentInfo(symbol)
	if err != nil {
		return err
	}
	return a.Storage.UpsertSymbolInfo(symbol, info)
}

// watchlistRefreshInfoHandler backfills market.InstrumentInfo for every watchlist symbol that
// doesn't have one yet (see symbolsMissingInfo) — the one-time catch-up for symbols added
// before this feature existed; new adds already fetch it inline (watchlistPageHandler).
// Sequential and synchronous: this runs once per deliberate button click, not on every page
// render, so paying provider latency here — unlike the live-recompute-on-every-request bugs
// already fixed on this dashboard (2cd6683, and the Alerts page) — is the expected cost of the
// action the click asked for, not a regression of the same kind.
func (a *Application) watchlistRefreshInfoHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	items, err := a.Watchlist.List()
	if err != nil {
		a.renderWatchlistPage(w, r, err.Error(), "")
		return
	}
	existing, err := a.Storage.SymbolInfoByTicker()
	if err != nil {
		a.renderWatchlistPage(w, r, err.Error(), "")
		return
	}

	missing := symbolsMissingInfo(items, existing)
	updated, failed := 0, 0
	for _, symbol := range missing {
		if err := a.fetchAndSaveSymbolInfo(symbol); err != nil {
			a.Logger.Warn("failed to fetch symbol info", zap.String("symbol", symbol), zap.Error(err))
			failed++
			continue
		}
		updated++
	}

	message := fmt.Sprintf("Fetched info for %d symbol(s).", updated)
	switch {
	case len(missing) == 0:
		message = "Every symbol already has info."
	case failed > 0:
		message += fmt.Sprintf(" %d failed — try again in a moment.", failed)
	}
	a.renderWatchlistPage(w, r, "", message)
}

// symbolsMissingInfo reports which watchlist symbols have no recorded market.InstrumentInfo yet
// — split out from watchlistRefreshInfoHandler so it's testable without a database behind it,
// same reasoning as countActionable/filterCurrentAlerts above.
func symbolsMissingInfo(items []watchlist.Item, existing map[string]persistence.SymbolInfo) []string {
	var missing []string
	for _, item := range items {
		if _, ok := existing[item.Symbol]; !ok {
			missing = append(missing, item.Symbol)
		}
	}
	return missing
}

// watchlistDeleteHandler removes a symbol — the dashboard equivalent of DELETE /watchlist (see
// watchlist.UseCase.Remove).
func (a *Application) watchlistDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderWatchlistPage(w, r, "Invalid form submission.", "")
		return
	}
	symbol := strings.ToUpper(strings.TrimSpace(r.FormValue("symbol")))
	if err := a.Watchlist.Remove(symbol); err != nil {
		a.renderWatchlistPage(w, r, err.Error(), "")
		return
	}
	a.renderWatchlistPage(w, r, "", "Removed "+symbol+" from the watchlist.")
}

// watchlistBulkDeleteHandler removes several watchlist symbols at once — the checkbox-select
// "Delete selected" action (see watchlist.UseCase.RemoveMany). Reads r.Form["symbols"] rather
// than a single FormValue, one per checked checkbox, distinct from the per-row Remove buttons
// in the same template, which still submit a single "symbol" to watchlistDeleteHandler via
// their own formaction.
func (a *Application) watchlistBulkDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderWatchlistPage(w, r, "Invalid form submission.", "")
		return
	}
	symbols := r.Form["symbols"]
	if len(symbols) == 0 {
		a.renderWatchlistPage(w, r, "No symbols selected.", "")
		return
	}
	if err := a.Watchlist.RemoveMany(symbols); err != nil {
		a.renderWatchlistPage(w, r, err.Error(), "")
		return
	}
	a.renderWatchlistPage(w, r, "", fmt.Sprintf("%d symbol(s) removed from the watchlist.", len(symbols)))
}

func (a *Application) renderWatchlistPage(w http.ResponseWriter, r *http.Request, errMsg, successMsg string) {
	items, err := a.Watchlist.List()
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	web.Render(w, "watchlist", web.WatchlistPage{
		Lang: a.resolveLang(r), Items: items, Info: a.loadSymbolInfo(), Error: errMsg, Success: successMsg,
	})
}

// loadSymbolInfo reads one symbols row per ticker (see fetchAndSaveSymbolInfo/
// refreshWatchlistQuotes) — Name/Type for the Watchlist page, LastPrice/PreviousClose for the
// Alerts page's "Today" column (see alertsPageHandler); each page's template only reads the
// fields it renders. One query serves both since it's the same underlying row. Best-effort like
// modelOptions above: a failed query costs those columns and nothing else, since neither page's
// core function depends on this.
func (a *Application) loadSymbolInfo() map[string]web.SymbolInfo {
	if a.Storage == nil {
		return nil
	}
	stored, err := a.Storage.SymbolInfoByTicker()
	if err != nil {
		a.Logger.Warn("failed to load symbol info", zap.Error(err))
		return nil
	}
	info := make(map[string]web.SymbolInfo, len(stored))
	for ticker, symbolInfo := range stored {
		info[ticker] = web.SymbolInfo{
			Name: symbolInfo.Name, Type: symbolInfo.AssetClass,
			LastPrice: symbolInfo.LastPrice, PreviousClose: symbolInfo.PreviousClose,
		}
	}
	return info
}

// accountPageHandler shows the current 2FA status and the enable/disable entry points — see
// docs/deployment.md's Implementation status note: 2FA is managed here, from the dashboard,
// not a static env-var secret.
func (a *Application) accountPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	a.renderAccountPage(w, r, "", "")
}

func (a *Application) renderAccountPage(w http.ResponseWriter, r *http.Request, errMsg, successMsg string) {
	enabled, err := a.Auth.TOTPStatus()
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	trainerConfigured := a.Trainer != nil
	var trainerStatus web.TrainerStatus
	var trainerErr string
	if trainerConfigured {
		if status, err := a.Trainer.Status(); err != nil {
			trainerErr = err.Error()
		} else {
			trainerStatus = web.TrainerStatus{
				Running: status.Running, StartedAt: status.StartedAt, FinishedAt: status.FinishedAt,
				ExitCode: status.ExitCode, Output: status.Output, Failed: status.Failed,
			}
		}
	}

	var hasBackup bool
	var lastBackupAt time.Time
	if a.Storage != nil {
		if at, ok, err := a.Storage.LastBackupAt(); err != nil {
			a.Logger.Warn("failed to read last backup time", zap.Error(err))
		} else {
			hasBackup, lastBackupAt = ok, at
		}
	}

	web.Render(w, "account", web.AccountPage{
		Lang:                  a.resolveLang(r),
		Username:              a.Auth.Username,
		TOTPEnabled:           enabled,
		Error:                 errMsg,
		Success:               successMsg,
		CorrectionModels:      a.modelOptions(mlUC.RoleCorrection, a.ML.CurrentModel()),
		RecoveryModels:        a.modelOptions(mlUC.RoleRecovery, a.ML.CurrentRecoveryModel()),
		CorrectionModelActive: a.ML.CurrentModel() != nil,
		RecoveryModelActive:   a.ML.CurrentRecoveryModel() != nil,
		MLEnabled:             a.Orchestrator.MLEnabled(),
		TrainerConfigured:     trainerConfigured,
		TrainerStatus:         trainerStatus,
		TrainerError:          trainerErr,
		HasBackup:             hasBackup,
		LastBackupAt:          lastBackupAt,
	})
}

// modelOptions lists every trained model file available for role (see ml.ListModels) as
// web.ModelOption values for the Account page's dropdown, marking whichever one matches
// active's Version as the currently-loaded one. active is nil when that role has no model
// loaded (the deterministic stub/zero default applies) — every option is then simply not
// active, not an error. A directory-read failure logs and renders an empty list rather than
// failing the whole Account page — the model picker is an enhancement, not a hard dependency.
func (a *Application) modelOptions(role mlUC.Role, active *mlUC.OnnxModel) []web.ModelOption {
	infos, err := mlUC.ListModels(a.Config.MLModelsDir, role)
	if err != nil {
		a.Logger.Warn("failed to list ML models", zap.String("role", string(role)), zap.Error(err))
		return nil
	}
	options := make([]web.ModelOption, len(infos))
	for i, info := range infos {
		options[i] = web.ModelOption{
			Filename:    info.Filename,
			Description: info.Description(),
			Active:      active != nil && info.Version == active.Version,
		}
	}
	return options
}

// mlEngineToggleHandler flips the decision engine's ML-blend toggle from the Account page — the
// switch takes effect immediately (decision.Orchestrator.SetMLEnabled hot-swaps a runtime flag,
// no restart needed) and is durably recorded (migrations/015) so it survives one. Model files
// stay loaded and usable via /predict either way; this only controls whether BuildRecommendation
// factors them into BUY/SELL/HOLD.
func (a *Application) mlEngineToggleHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderAccountPage(w, r, "Invalid form submission.", "")
		return
	}
	enabled := r.FormValue("enabled") == "true"

	a.Orchestrator.SetMLEnabled(enabled)
	if err := a.Storage.SetMLEnabled(enabled); err != nil {
		// The toggle already took effect in memory; only the durable record failed, so this
		// warns rather than rolling the toggle back — a restart would just re-read ML_ENABLED's
		// default in that case, not silently lose the running setting, same stance as
		// mlModelActivateHandler's SetActiveModelSelection failure below.
		a.Logger.Warn("failed to persist ML-enabled setting", zap.Bool("enabled", enabled), zap.Error(err))
	}

	if enabled {
		a.renderAccountPage(w, r, "", "ML blend enabled — recommendations now factor in ML/portfolio/risk again.")
	} else {
		a.renderAccountPage(w, r, "", "ML blend disabled — recommendations now score from the technical term alone.")
	}
}

// mlModelActivateHandler loads the model the Account page's dropdown submitted and, on success,
// hot-swaps it into the running ML use case (no restart needed — see ml.UseCase.SetModel) and
// durably records the choice (migrations/011) so it survives one. The submitted filename is
// re-validated against a fresh ml.ListModels scan rather than trusted as-is, so this can't be
// used to load an arbitrary path outside ML_MODELS_DIR.
func (a *Application) mlModelActivateHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderAccountPage(w, r, "Invalid form submission.", "")
		return
	}
	roleValue := r.FormValue("role")
	filename := r.FormValue("filename")

	role, ok := parseModelRole(roleValue)
	if !ok {
		a.renderAccountPage(w, r, "Unknown model role.", "")
		return
	}

	infos, err := mlUC.ListModels(a.Config.MLModelsDir, role)
	if err != nil {
		a.renderAccountPage(w, r, "Could not read the models directory: "+err.Error(), "")
		return
	}
	found := false
	for _, info := range infos {
		if info.Filename == filename {
			found = true
			break
		}
	}
	if !found {
		a.renderAccountPage(w, r, "That model is no longer available — refresh and try again.", "")
		return
	}

	path := a.Config.MLModelsDir + "/" + filename
	model, err := mlUC.LoadOnnxModel(path, a.Config.LibOnnxRuntimePath)
	if err != nil {
		a.renderAccountPage(w, r, "Failed to load that model: "+err.Error(), "")
		return
	}

	var previous *mlUC.OnnxModel
	if role == mlUC.RoleCorrection {
		previous = a.ML.SetModel(model)
	} else {
		previous = a.ML.SetRecoveryModel(model)
	}
	if err := a.Storage.SetActiveModelSelection(string(role), filename); err != nil {
		// The swap already took effect in memory; only the durable record failed, so this
		// warns rather than rolling the swap back — a restart would just re-read MODEL_PATH's
		// default in that case, not silently lose the running model.
		a.Logger.Warn("failed to persist ML model selection", zap.String("role", string(role)), zap.Error(err))
	}
	if previous != nil {
		if err := previous.Close(); err != nil {
			a.Logger.Warn("failed to close previous ML model", zap.String("role", string(role)), zap.Error(err))
		}
	}

	a.renderAccountPage(w, r, "", "Activated model "+model.Version+" for "+roleValue+".")
}

// parseModelRole validates a form-submitted role value against the two real roles ml.ListModels
// understands — used by both mlModelActivateHandler and mlModelDeleteHandler so an unrecognized
// value is rejected the same way in either place.
func parseModelRole(value string) (mlUC.Role, bool) {
	switch value {
	case string(mlUC.RoleCorrection):
		return mlUC.RoleCorrection, true
	case string(mlUC.RoleRecovery):
		return mlUC.RoleRecovery, true
	default:
		return "", false
	}
}

// mlModelDeleteHandler removes a trained model file the Account page's dropdown submitted —
// via the trainer sidecar (see internal/infrastructure/trainer), since atlas-api's own
// ./training/models mount is read-only by design (docker-compose.yml). Refuses to delete
// whichever model is currently active for that role: that would leave the running (in-memory)
// model's file gone from disk, breaking a future restart or re-activation without anything
// visibly wrong right now — the safer failure mode is refusing up front.
func (a *Application) mlModelDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if a.Trainer == nil {
		a.renderAccountPage(w, r, "Deleting models isn't available (TRAINER_URL is unset) — see training/README.md, or remove the file by hand from training/models/.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderAccountPage(w, r, "Invalid form submission.", "")
		return
	}
	roleValue := r.FormValue("role")
	filename := r.FormValue("filename")

	role, ok := parseModelRole(roleValue)
	if !ok {
		a.renderAccountPage(w, r, "Unknown model role.", "")
		return
	}

	active := a.ML.CurrentModel()
	if role == mlUC.RoleRecovery {
		active = a.ML.CurrentRecoveryModel()
	}
	found := false
	for _, info := range a.modelOptions(role, active) {
		if info.Filename != filename {
			continue
		}
		found = true
		if info.Active {
			a.renderAccountPage(w, r, "Can't remove the model currently active for "+roleValue+" — activate a different one first.", "")
			return
		}
	}
	if !found {
		a.renderAccountPage(w, r, "That model is no longer available — refresh and try again.", "")
		return
	}

	if err := a.Trainer.Delete(filename); err != nil {
		a.renderAccountPage(w, r, "Failed to delete that model: "+err.Error(), "")
		return
	}
	a.renderAccountPage(w, r, "", "Deleted "+filename+".")
}

// mlTrainHandler triggers a new training run via the atlas-trainer sidecar (see cmd/trainer,
// TRAINER_URL) — the real action behind the Account dashboard's "Train new candidate" button.
// This only ever produces a new, uniquely-versioned candidate model file
// (training/retrain.sh never touches which model is active); promoting one is still the
// separate mlModelActivateHandler action above.
func (a *Application) mlTrainHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if a.Trainer == nil {
		a.renderAccountPage(w, r, "Training trigger isn't configured (TRAINER_URL is unset) — see training/README.md to run it by hand, or start the atlas-trainer service to enable this button.", "")
		return
	}
	if err := a.Trainer.Trigger(); err != nil {
		a.renderAccountPage(w, r, "Failed to start training: "+err.Error(), "")
		return
	}
	a.renderAccountPage(w, r, "", "Training started — this can take a few minutes (longer on the very first run). Refresh this page to check progress.")
}

// backupOperationTimeout is how long accountBackupHandler/accountRestoreHandler/
// accountFlushHandler get past http.Server's default WriteTimeout (10s, cmd/api/main.go — sized
// for ordinary fast JSON/HTML responses). A full-database dump/restore/wipe can legitimately run
// longer than that once prices/indicators/recommendations have accumulated — confirmed live: a
// ~25MB restore was silently killed mid-request, the client just seeing an empty response.
const backupOperationTimeout = 2 * time.Minute

// extendWriteDeadline lifts the connection's write deadline for the handlers above — best-effort:
// logs and continues if the underlying ResponseWriter doesn't support deadlines, the same
// "optional enhancement, never a hard failure" stance the rest of this file takes for
// non-critical paths.
func (a *Application) extendWriteDeadline(w http.ResponseWriter, d time.Duration) {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(d)); err != nil {
		a.Logger.Warn("failed to extend write deadline", zap.Error(err))
	}
}

// accountBackupHandler streams every table persistence.TimescaleDB.BackupAll covers as one
// downloadable JSON file — a portable snapshot for disaster recovery or seeding a fresh
// environment. Read-only, so unlike Restore/Flush below it needs no confirmation dialog.
func (a *Application) accountBackupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	if a.Storage == nil {
		http.Error(w, "no database configured", http.StatusServiceUnavailable)
		return
	}
	a.extendWriteDeadline(w, backupOperationTimeout)
	payload, err := a.Storage.BackupAll()
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	filename := fmt.Sprintf("atlas-backup-%s.json", time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	_, _ = w.Write(payload)
}

// accountRestoreHandler replaces every row Atlas has with the contents of an uploaded
// accountBackupHandler file — the recovery path after accountFlushHandler, or how to seed a
// fresh environment from an existing one's backup.
func (a *Application) accountRestoreHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if a.Storage == nil {
		a.renderAccountPage(w, r, "No database configured.", "")
		return
	}
	a.extendWriteDeadline(w, backupOperationTimeout)
	if err := r.ParseMultipartForm(maxBackupRestoreBytes); err != nil {
		a.renderAccountPage(w, r, "Invalid form submission.", "")
		return
	}
	file, _, err := r.FormFile("backup")
	if err != nil {
		a.renderAccountPage(w, r, "Choose a backup file to restore.", "")
		return
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maxBackupRestoreBytes))
	if err != nil {
		a.renderAccountPage(w, r, "Failed to read the uploaded file: "+err.Error(), "")
		return
	}
	if err := a.Storage.RestoreAll(payload); err != nil {
		a.renderAccountPage(w, r, "Restore failed: "+err.Error(), "")
		return
	}
	a.reloadCachedState()
	a.renderAccountPage(w, r, "", "Data restored from backup.")
}

// accountFlushHandler wipes every table Atlas stores data in — positions, transactions,
// watchlist, price/indicator history, recommendations, ML/2FA settings — back to the state a
// freshly migrated database would be in (see persistence.TimescaleDB.FlushAll's doc comment for
// exactly what survives, notably login itself). Confirmed client-side (account.html) since there
// is no undo short of a prior backup.
func (a *Application) accountFlushHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if a.Storage == nil {
		a.renderAccountPage(w, r, "No database configured.", "")
		return
	}
	a.extendWriteDeadline(w, backupOperationTimeout)
	if err := a.Storage.FlushAll(); err != nil {
		a.renderAccountPage(w, r, "Flush failed: "+err.Error(), "")
		return
	}
	a.reloadCachedState()
	a.renderAccountPage(w, r, "", "All data has been wiped. Atlas is back to a freshly installed state.")
}

// reloadCachedState resyncs every in-memory cache that mirrors a database table (see
// portfolio.UseCase.Reload/watchlist.UseCase.Reload) after a bulk out-of-band change
// (accountFlushHandler/accountRestoreHandler) that per-mutation methods like RecordTransaction
// can't express — the same class of staleness TestRecordTransactionDefaultsTimestampWhenEmpty's
// bug came from, just at bulk scale instead of per-transaction. Logs rather than fails outright:
// the underlying data is already committed either way, so a stale cache is a display bug that
// would sort itself out on the next restart, not silent data loss. ML model *selection* (which
// trained file is active) is intentionally left alone here — resolveMLEnabled/model files
// themselves aren't touched by Flush/Restore, matching the existing "disable without deleting"
// design (see account.ml_engine_heading) — only the enabled/disabled toggle is resynced.
func (a *Application) reloadCachedState() {
	if err := a.Portfolio.Reload(); err != nil {
		a.Logger.Warn("failed to reload portfolio state after bulk data change", zap.Error(err))
	}
	if err := a.Watchlist.Reload(); err != nil {
		a.Logger.Warn("failed to reload watchlist state after bulk data change", zap.Error(err))
	}
	a.Orchestrator.SetMLEnabled(resolveMLEnabled(a.Storage, a.Logger, a.Config.MLEnabled))
}

// totpSetupHandler starts a new enrollment (authUC.UseCase.BeginTOTPEnrollment) and renders the
// QR-code confirmation page. Requires a valid session (like every /dashboard/* route), read via
// the same cookie requireAuth already validated.
func (a *Application) totpSetupHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	secret, err := a.Auth.BeginTOTPEnrollment(r.Context(), cookie.Value)
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	web.Render(w, "totp_enroll", web.TOTPEnrollPage{Lang: a.resolveLang(r), Secret: secret})
}

// totpQRHandler serves the pending enrollment's QR code as a PNG image — rendered via
// github.com/skip2/go-qrcode (QR encoding is a well-specified but genuinely intricate matrix/
// error-correction algorithm, unlike TOTP's straightforward HMAC construction, so a maintained
// library is the right call here rather than hand-rolling it). Looks up the same pending secret
// PendingTOTPSecret already stashed for this session, so the image matches whatever
// totpSetupHandler rendered the manual key for.
func (a *Application) totpQRHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	secret, found, err := a.Auth.PendingTOTPSecret(r.Context(), cookie.Value)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "no pending 2FA enrollment for this session — start again at /dashboard/account/totp/setup", http.StatusNotFound)
		return
	}
	uri := authUC.TOTPProvisioningURI("Atlas", a.Auth.Username, secret)
	png, err := qrcode.Encode(uri, qrcode.Medium, 256)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

// totpConfirmHandler verifies the code the operator typed in against the pending secret and, on
// success, durably activates 2FA (authUC.UseCase.ConfirmTOTPEnrollment) — this is the step that
// proves the authenticator app actually has the secret, not just that a QR code was shown once.
func (a *Application) totpConfirmHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		web.Render(w, "totp_enroll", web.TOTPEnrollPage{Lang: a.resolveLang(r), Error: "Invalid form submission."})
		return
	}
	if err := a.Auth.ConfirmTOTPEnrollment(r.Context(), cookie.Value, r.FormValue("code")); err != nil {
		secret, _, _ := a.Auth.PendingTOTPSecret(r.Context(), cookie.Value)
		web.Render(w, "totp_enroll", web.TOTPEnrollPage{Lang: a.resolveLang(r), Secret: secret, Error: "Incorrect code — try again."})
		return
	}
	a.renderAccountPage(w, r, "", "Two-factor authentication is now enabled.")
}

// totpDisableHandler turns 2FA off after re-verifying the current password (authUC.UseCase.
// DisableTOTP) — a real re-auth check, not just "there's a valid session cookie", since this
// weakens the account.
func (a *Application) totpDisableHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		a.renderAccountPage(w, r, "Invalid form submission.", "")
		return
	}
	if err := a.Auth.DisableTOTP(r.FormValue("password")); err != nil {
		a.renderAccountPage(w, r, "Incorrect password.", "")
		return
	}
	a.renderAccountPage(w, r, "", "Two-factor authentication has been disabled.")
}

// watchlistImportPageHandler applies an uploaded watchlist export — the dashboard counterpart of
// POST /watchlist/import, and the path that actually matters for the feature's purpose: moving a
// real watchlist between environments without re-entering several dozen symbols by hand.
//
// The file is read through a bounded reader rather than trusting the multipart form's own limit,
// and the destructive mode has to arrive explicitly as "replace" — a missing or unrecognised
// value merges, so a mangled form can never silently clear the watchlist.
func (a *Application) watchlistImportPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseMultipartForm(maxWatchlistImportBytes); err != nil {
		a.renderWatchlistPage(w, r, web.T(a.resolveLang(r), "watchlist.import_invalid"), "")
		return
	}
	mode, err := watchlistUC.ParseImportMode(r.FormValue("mode"))
	if err != nil {
		a.renderWatchlistPage(w, r, err.Error(), "")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		a.renderWatchlistPage(w, r, web.T(a.resolveLang(r), "watchlist.import_no_file"), "")
		return
	}
	defer file.Close()

	body, err := io.ReadAll(io.LimitReader(file, maxWatchlistImportBytes))
	if err != nil {
		a.renderWatchlistPage(w, r, err.Error(), "")
		return
	}
	items, err := decodeWatchlistImport(body)
	if err != nil {
		a.renderWatchlistPage(w, r, err.Error(), "")
		return
	}
	result, err := a.Watchlist.Import(items, mode, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		a.renderWatchlistPage(w, r, err.Error(), "")
		return
	}
	a.renderWatchlistPage(w, r, "", fmt.Sprintf(
		web.T(a.resolveLang(r), "watchlist.import_done"), result.Added, result.Skipped, result.Removed))
}
