package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"atlas/internal/app/web"
	portfolioUC "atlas/internal/application/portfolio"
	"atlas/internal/domain/currency"
	"atlas/internal/domain/portfolio"
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
	scoped, err := a.forPortfolio(r)
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	a = scoped
	positions, transactions, activePortfolioID, portfolios := a.Portfolio.Snapshot()
	livePositions, asOf := a.livePricedPositions(positions, priceAsOf(transactions))
	web.Render(w, "dashboard", web.DashboardPage{
		Lang: a.resolveLang(r), Positions: livePositions, Risk: portfolioUC.ComputeRiskSnapshot(livePositions),
		Rates:             a.exchangeRatesForDisplay(),
		PriceAsOf:         asOf,
		AlertCounts:       a.alertCountsByAction(),
		Portfolios:        portfolios,
		ActivePortfolioID: activePortfolioID,
	})
}

// portfolioPageHandler lists positions (GET) and records a transaction (POST) — the dashboard
// equivalent of GET/POST /portfolio: recording BUY/SELL without hand-crafting an HTTP request.
func (a *Application) portfolioPageHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.renderPortfolioPage(w, r, "", "")
	case http.MethodPost:
		scoped, err := a.forPortfolio(r)
		if err != nil {
			a.renderError(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		a = scoped
		if err := r.ParseForm(); err != nil {
			a.renderPortfolioPage(w, r, a.tr(r, "msg.invalid_form"), "")
			return
		}
		form := web.TransactionForm{
			Key:         strings.TrimSpace(r.FormValue("key")),
			PortfolioID: r.FormValue("portfolio_id"),
			Symbol:      strings.ToUpper(strings.TrimSpace(r.FormValue("symbol"))),
			Type:        r.FormValue("type"),
			Quantity:    r.FormValue("quantity"),
			Price:       r.FormValue("price"),
			Currency:    r.FormValue("currency"),
			Date:        strings.TrimSpace(r.FormValue("date")),
		}
		rejected := func(message string) {
			a.renderPortfolioPageWithForm(w, r, message, "", &form)
		}
		// The record-transaction form's own portfolio picker (only shown with >1 portfolio) can
		// name a different portfolio than whatever tab is currently active — switch to it first
		// so the transaction lands where the form actually said, and update the cookie so the
		// page the user lands on after this keeps showing that same portfolio.
		if portfolioID := r.FormValue("portfolio_id"); portfolioID != "" && portfolioID != a.Portfolio.ActivePortfolio() {
			if err := a.Portfolio.SwitchPortfolio(portfolioID); err != nil {
				rejected(err.Error())
				return
			}
			setActivePortfolioCookie(w, portfolioID)
		}
		quantity, qErr := strconv.ParseFloat(form.Quantity, 64)
		price, pErr := strconv.ParseFloat(form.Price, 64)
		if qErr != nil || pErr != nil {
			rejected(a.tr(r, "msg.qty_price_numbers"))
			return
		}
		if form.Key == "" {
			// Every rendered form carries one; its absence means a hand-built request, and
			// recording without a key is exactly the duplicate risk the key exists to remove.
			rejected(a.tr(r, "msg.form_expired"))
			return
		}
		tx := portfolio.Transaction{
			ID:       form.Key,
			Symbol:   form.Symbol,
			Type:     form.Type,
			Quantity: quantity,
			Price:    price,
			Currency: form.Currency,
		}
		// The trade date, which the conversion to PLN depends on: a backdated USD/EUR trade is
		// priced at that day's NBP rate, not today's. Today's date keeps the exact current time
		// (an empty Timestamp), so a same-day entry is not moved to midnight.
		if form.Date != "" && form.Date != web.Today() {
			tx.Timestamp = form.Date
		}
		if err := a.Portfolio.RecordTransaction(tx); err != nil {
			rejected(err.Error())
			return
		}
		go a.refreshRecommendation(tx.Symbol)
		a.flashRedirect(w, r, "/dashboard/portfolio", a.tr(r, "msg.transaction_recorded"))
	default:
		methodNotAllowed(w)
	}
}

// formValues reads a checkbox-select bulk field (one repeated "plural" entry per checked box)
// with a single-item fallback: a per-row Remove button submits one "singular" value instead, and
// both land on the same handler. A present singular value wins outright, so clicking one row's
// Remove while unrelated boxes are still checked acts on that row alone, exactly as it did when
// the two were separate endpoints.
func formValues(r *http.Request, plural, singular string) []string {
	if one := strings.TrimSpace(r.FormValue(singular)); one != "" {
		return []string{one}
	}
	return r.Form[plural]
}

// upperAll normalizes submitted symbols to the uppercase form positions and watchlist items are
// stored under. Deliberately not applied to transaction ids, which are opaque UUIDs that an
// uppercase pass would simply corrupt into a no-match.
func upperAll(values []string) []string {
	normalized := make([]string, len(values))
	for i, v := range values {
		normalized[i] = strings.ToUpper(strings.TrimSpace(v))
	}
	return normalized
}

// portfolioDeleteHandler removes positions entirely (e.g. a test entry recorded by mistake) —
// the dashboard equivalent of DELETE /portfolio. Distinct from recording a SELL: this clears the
// current position aggregate without adding a transaction record (see
// portfolio.UseCase.RemovePositions). Serves both the Positions table's checkbox-select "Delete
// selected" and its per-row Remove buttons — see formValues.
func (a *Application) portfolioDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	scoped, err := a.forPortfolio(r)
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	a = scoped
	if err := r.ParseForm(); err != nil {
		a.renderPortfolioPage(w, r, a.tr(r, "msg.invalid_form"), "")
		return
	}
	symbols := upperAll(formValues(r, "symbols", "symbol"))
	if len(symbols) == 0 {
		a.renderPortfolioPage(w, r, a.tr(r, "msg.no_positions_selected"), "")
		return
	}
	if err := a.Portfolio.RemovePositions(symbols); err != nil {
		a.renderPortfolioPage(w, r, err.Error(), "")
		return
	}
	// Every removed symbol gets its recommendation refreshed, not just the single-delete case:
	// dropping a position changes the portfolio/risk term the decision engine blends in (see
	// decision.Orchestrator.blendMLAndPortfolio), so a stale recommendation would otherwise
	// survive a bulk delete.
	for _, symbol := range symbols {
		go a.refreshRecommendation(symbol)
	}
	message := a.tr(r, "msg.positions_hidden_many", len(symbols))
	if len(symbols) == 1 {
		message = a.tr(r, "msg.position_hidden_one", symbols[0])
	}
	a.renderPortfolioPage(w, r, "", message)
}

// portfolioTransactionDeleteHandler removes historical transactions by id (see
// portfolio.UseCase.RemoveTransactions) — the dashboard equivalent of DELETE /transactions.
// Distinct from portfolioDeleteHandler: this touches the transaction history itself and
// recomputes each affected position from what remains, rather than just clearing the current
// aggregate. Serves both the checkbox-select "Delete selected" and the per-row delete buttons —
// see formValues.
func (a *Application) portfolioTransactionDeleteHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	scoped, err := a.forPortfolio(r)
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	a = scoped
	if err := r.ParseForm(); err != nil {
		a.renderPortfolioPage(w, r, a.tr(r, "msg.invalid_form"), "")
		return
	}
	ids := formValues(r, "ids", "id")
	if len(ids) == 0 {
		a.renderPortfolioPage(w, r, a.tr(r, "msg.no_transactions_selected"), "")
		return
	}
	// Resolved before the removal, not after — once the transaction is gone there's nothing left
	// to look its symbol up from.
	symbols := make([]string, 0, len(ids))
	for _, id := range ids {
		if symbol := a.transactionSymbol(id); symbol != "" {
			symbols = append(symbols, symbol)
		}
	}
	if err := a.Portfolio.RemoveTransactions(ids); err != nil {
		a.renderPortfolioPage(w, r, err.Error(), "")
		return
	}
	for _, symbol := range symbols {
		go a.refreshRecommendation(symbol)
	}
	message := a.tr(r, "msg.transactions_removed_many", len(ids))
	if len(ids) == 1 {
		message = a.tr(r, "msg.transaction_removed_one")
	}
	a.renderPortfolioPage(w, r, "", message)
}

func (a *Application) renderPortfolioPage(w http.ResponseWriter, r *http.Request, errMsg, successMsg string) {
	a.renderPortfolioPageWithForm(w, r, errMsg, successMsg, nil)
}

// renderPortfolioPageWithForm renders the page with the record-transaction form set to form, or
// to a fresh one (new key, today's date) when form is nil — see web.PortfolioPage.Form.
func (a *Application) renderPortfolioPageWithForm(w http.ResponseWriter, r *http.Request, errMsg, successMsg string, form *web.TransactionForm) {
	if form == nil {
		key, err := newFormKey()
		if err != nil {
			a.renderError(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		form = &web.TransactionForm{Key: key, Type: "BUY", Currency: "PLN", Date: web.Today()}
	}
	form.Today = web.Today()
	if successMsg == "" {
		successMsg = readFlash(w, r)
	}
	scoped, err := a.forPortfolio(r)
	if err != nil {
		a.renderError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	a = scoped
	positions, transactions, activePortfolioID, portfolios := a.Portfolio.Snapshot()
	livePositions, asOf := a.livePricedPositions(positions, priceAsOf(transactions))
	data := web.PortfolioPage{
		Lang: a.resolveLang(r), Positions: livePositions, Transactions: transactions,
		Rates: a.exchangeRatesForDisplay(), Currencies: currencyOptions(),
		PriceAsOf: asOf,
		Error:     errMsg, Success: successMsg,
		Portfolios:        portfolios,
		ActivePortfolioID: activePortfolioID,
		Form:              *form,
	}
	if form.PortfolioID == "" {
		data.Form.PortfolioID = activePortfolioID
	}
	if symbols, err := a.watchlistSymbols(); err == nil {
		data.Symbols = symbols
	}
	web.Render(w, "portfolio", data)
}

// livePricedPositions turns the shared valuation (see portfolio.Position's PricedAt/PriceSource,
// resolved once in the SQL layer) into the per-symbol "as of" map the templates render.
//
// It used to run a second query and overlay quotes itself, which is exactly how the dashboard
// and the decision engine came to disagree about what a position was worth. There is now one
// valuation and this only formats it. Positions still priced from their last trade keep the
// transaction-time fallback, so the page says when the number is from rather than implying it
// is current.
func (a *Application) livePricedPositions(positions []portfolio.Position, asOf map[string]string) ([]portfolio.Position, map[string]string) {
	live := make(map[string]string, len(asOf)+len(positions))
	for symbol, ts := range asOf {
		live[symbol] = ts
	}
	for _, position := range positions {
		if position.PriceSource == portfolio.PriceFromQuote && !position.PricedAt.IsZero() {
			live[position.Symbol] = position.PricedAt.Format(time.RFC3339)
		}
	}
	return positions, live
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

// backupOperationTimeout is how long accountBackupHandler/accountRestoreHandler/
// accountFlushHandler get past http.Server's default WriteTimeout (10s, cmd/api/main.go — sized
// for ordinary fast JSON/HTML responses). A full-database dump/restore/wipe can legitimately run
// longer than that once prices/indicators/recommendations have accumulated — confirmed live: a
// ~25MB restore was silently killed mid-request, the client just seeing an empty response.
const backupOperationTimeout = 2 * time.Minute

// newFormKey issues a record-transaction form's idempotency key — a random UUID, the shape
// portfolio.Transaction.ID requires. See web.TransactionForm.Key.
func newFormKey() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate form key: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
