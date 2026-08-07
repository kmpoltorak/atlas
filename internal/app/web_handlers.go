package app

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"atlas/internal/app/web"
	authUC "atlas/internal/application/auth"
	"atlas/internal/domain/currency"
	"atlas/internal/domain/portfolio"

	qrcode "github.com/skip2/go-qrcode"
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

// currencyOptions lists the currencies the record-transaction form's dropdown offers, as plain
// strings for templates.
func currencyOptions() []string {
	all := currency.All()
	options := make([]string, len(all))
	for i, code := range all {
		options[i] = string(code)
	}
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
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		}
		requiresTOTP, err := a.Auth.TOTPStatus()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
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
				http.Error(w, statusErr.Error(), http.StatusInternalServerError)
				return
			}
			web.Render(w, "login", web.LoginPage{Lang: lang, Error: "Invalid username, password, or verification code.", RequiresTOTP: requiresTOTP})
			return
		}
		http.SetCookie(w, &http.Cookie{
			Name: sessionCookieName, Value: token, Path: "/",
			HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(authUC.SessionTTL.Seconds()),
		})
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
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

func (a *Application) dashboardPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	positions, err := a.Portfolio.GetPositions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	risk, err := a.Portfolio.ComputeRisk()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	web.Render(w, "dashboard", web.DashboardPage{
		Lang: a.resolveLang(r), Positions: positions, Risk: risk, Rates: a.exchangeRatesForDisplay(),
	})
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
	a.renderPortfolioPage(w, r, "", "Removed "+symbol+" from your positions.")
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
	if err := a.Portfolio.RemoveTransaction(id); err != nil {
		a.renderPortfolioPage(w, r, err.Error(), "")
		return
	}
	a.renderPortfolioPage(w, r, "", "Transaction removed and position recomputed.")
}

func (a *Application) renderPortfolioPage(w http.ResponseWriter, r *http.Request, errMsg, successMsg string) {
	positions, err := a.Portfolio.GetPositions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	transactions, err := a.Portfolio.GetTransactions()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	risk, err := a.Portfolio.ComputeRisk()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	web.Render(w, "portfolio", web.PortfolioPage{
		Lang: a.resolveLang(r), Positions: positions, Transactions: transactions, Risk: risk,
		Rates: a.exchangeRatesForDisplay(), Currencies: currencyOptions(),
		Error: errMsg, Success: successMsg,
	})
}

// watchlistSymbols returns the watchlist as a plain []string — used to populate the
// recommendations/backtests page dropdowns so the common case doesn't require typing a ticker
// by hand.
func (a *Application) watchlistSymbols() ([]string, error) {
	items, err := a.Watchlist.List()
	if err != nil {
		return nil, err
	}
	symbols := make([]string, len(items))
	for i, item := range items {
		symbols[i] = item.Symbol
	}
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

// alertsPageHandler mirrors alertsHandler (GET /alerts) — same evaluation, same persistence.
func (a *Application) alertsPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	result, err := a.Alerts.Evaluate()
	for _, alert := range result {
		a.persistRecommendation(alert.Recommendation)
	}
	data := web.AlertsPage{Lang: a.resolveLang(r), Alerts: result}
	if err != nil {
		data.Warning = err.Error()
	}
	web.Render(w, "alerts", data)
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
		a.renderWatchlistPage(w, r, "", "Added "+symbol+" to the watchlist.")
	default:
		methodNotAllowed(w)
	}
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

func (a *Application) renderWatchlistPage(w http.ResponseWriter, r *http.Request, errMsg, successMsg string) {
	items, err := a.Watchlist.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	web.Render(w, "watchlist", web.WatchlistPage{Lang: a.resolveLang(r), Items: items, Error: errMsg, Success: successMsg})
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	web.Render(w, "account", web.AccountPage{Lang: a.resolveLang(r), Username: a.Auth.Username, TOTPEnabled: enabled, Error: errMsg, Success: successMsg})
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
		http.Error(w, err.Error(), http.StatusInternalServerError)
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
