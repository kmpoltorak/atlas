package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"atlas/internal/domain/alerts"
	backtestingDomain "atlas/internal/domain/backtesting"
	"atlas/internal/domain/decision"
	"atlas/internal/domain/portfolio"
	"atlas/internal/domain/watchlist"
)

func TestRenderDashboardPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "dashboard", DashboardPage{
		Positions: []portfolio.Position{{Symbol: "AAPL", Quantity: 10, AverageCost: 100, CurrentPrice: 120}},
		Risk:      portfolio.RiskSnapshot{Exposure: 1200, Drawdown: -0.1, Volatility: 15},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "AAPL") || !strings.Contains(body, "200.00") {
		t.Fatalf("expected position row with computed P&L (200.00) in rendered dashboard, got %s", body)
	}
	// Default (zero-value) Lang must render English, not blank/raw keys.
	if !strings.Contains(body, "Dashboard") || !strings.Contains(body, "Portfolio Risk") {
		t.Fatalf("expected English text with a zero-value Lang, got %s", body)
	}
}

func TestMoneyShowsPLNOnlyWithoutRates(t *testing.T) {
	got := string(money(100, nil))
	if got != "100.00 PLN" {
		t.Fatalf("expected plain PLN with no rates, got %q", got)
	}
}

func TestMoneyShowsEURAndUSDConversion(t *testing.T) {
	got := string(money(400, map[string]float64{"EUR": 4.0, "USD": 3.73}))
	if !strings.Contains(got, "400.00 PLN") {
		t.Fatalf("expected the primary PLN figure, got %q", got)
	}
	if !strings.Contains(got, "100.00 EUR") {
		t.Fatalf("expected 400/4.0 = 100.00 EUR, got %q", got)
	}
	if !strings.Contains(got, "107.24 USD") {
		t.Fatalf("expected 400/3.73 ≈ 107.24 USD, got %q", got)
	}
}

func TestMoneyIgnoresAZeroOrMissingRate(t *testing.T) {
	got := string(money(100, map[string]float64{"EUR": 0}))
	if strings.Contains(got, "EUR") {
		t.Fatalf("expected a zero rate to be skipped rather than divide-by-zero, got %q", got)
	}
}

func TestRenderDashboardPageShowsCurrencyConversion(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "dashboard", DashboardPage{
		Positions: []portfolio.Position{{Symbol: "AAPL", Quantity: 10, AverageCost: 100, CurrentPrice: 120}},
		Rates:     map[string]float64{"EUR": 4.0, "USD": 3.73},
	})
	body := rec.Body.String()
	if !strings.Contains(body, "25.00 EUR") { // 100 PLN avg cost / 4.0
		t.Fatalf("expected the position's average cost converted to EUR, got %s", body)
	}
}

func TestRenderDashboardPageInPolish(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "dashboard", DashboardPage{Lang: LangPL})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Panel główny") || !strings.Contains(body, "Ryzyko portfela") {
		t.Fatalf("expected Polish text with Lang: LangPL, got %s", body)
	}
	if !strings.Contains(body, "Brak otwartych pozycji") {
		t.Fatalf("expected the Polish no-positions message (with its embedded link), got %s", body)
	}
}

func TestRenderNavLanguageSwitcherMarksActiveLanguage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "dashboard", DashboardPage{Lang: LangPL})
	body := rec.Body.String()
	if !strings.Contains(body, `value="pl"`) || !strings.Contains(body, `value="en"`) ||
		!strings.Contains(body, `value="de"`) || !strings.Contains(body, `value="fr"`) ||
		!strings.Contains(body, `value="es"`) {
		t.Fatalf("expected all five language options in the nav switcher, got %s", body)
	}
	if !strings.Contains(body, `dropdown-item active">Polski`) {
		t.Fatalf("expected the Polish option to be marked active when Lang is pl, got %s", body)
	}
}

func TestRenderPortfolioPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "portfolio", PortfolioPage{
		Success: "Transaction recorded",
		Transactions: []portfolio.Transaction{
			{ID: "tx-1", Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Timestamp: "2026-01-01T00:00:00Z"},
		},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Transaction recorded") {
		t.Fatalf("expected success message in rendered portfolio page, got %s", body)
	}
	if !strings.Contains(body, "tx-1") || !strings.Contains(body, `action="/dashboard/portfolio/transactions/delete"`) {
		t.Fatalf("expected the transaction row and its delete form in rendered portfolio page, got %s", body)
	}
}

func TestRenderPortfolioPageShowsCurrencyDropdownAndTransactionCurrency(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "portfolio", PortfolioPage{
		Currencies: []string{"PLN", "EUR", "USD"},
		Transactions: []portfolio.Transaction{
			{ID: "tx-1", Symbol: "AAPL", Type: "BUY", Quantity: 10, Price: 100, Currency: "USD", PriceInPLN: 373, Timestamp: "2026-01-01T00:00:00Z"},
		},
		// The handler's fresh form (renderPortfolioPageWithForm) defaults to PLN.
		Form: TransactionForm{Currency: "PLN", Type: "BUY"},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `<option value="PLN" selected>`) || !strings.Contains(body, `<option value="EUR">`) || !strings.Contains(body, `<option value="USD">`) {
		t.Fatalf("expected a currency dropdown with PLN/EUR/USD (PLN selected by default), got %s", body)
	}
	if !strings.Contains(body, ">USD<") || !strings.Contains(body, "373.00") {
		t.Fatalf("expected the transaction row to show its original currency and stored PLN-equivalent price, got %s", body)
	}
}

func TestRenderPortfolioPageWithoutTransactionsShowsEmptyState(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "portfolio", PortfolioPage{})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "No transactions recorded yet.") {
		t.Fatalf("expected an empty-state message, got %s", rec.Body.String())
	}
}

func TestRenderRecommendationsPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "recommendations", RecommendationsPage{
		Symbol:  "AAPL",
		Symbols: []string{"AAPL", "MSFT"},
		Recommendation: &decision.Recommendation{
			Symbol: "AAPL", Action: "BUY", Confidence: 0.78, Reason: []string{"Momentum signal is favorable"},
			Score: 0.81, MarketCondition: "BULL",
		},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "BUY") || !strings.Contains(body, "BULL") || !strings.Contains(body, "Momentum signal is favorable") {
		t.Fatalf("expected action/market condition/reason in rendered recommendations page, got %s", body)
	}
	if !strings.Contains(body, `<option value="AAPL" selected>AAPL</option>`) || !strings.Contains(body, `<option value="MSFT" >MSFT</option>`) {
		t.Fatalf("expected a symbol dropdown with AAPL selected, got %s", body)
	}
	if !strings.Contains(body, `class="text-success fw-semibold"`) {
		t.Fatalf("expected BUY to use Bootstrap's text-success utility class (which carries "+
			"!important, needed to beat the table component's own higher-specificity color "+
			"rule), got %s", body)
	}
}

func TestActionClass(t *testing.T) {
	tests := map[string]string{
		"BUY": "text-success fw-semibold", "ACCUMULATE": "text-success fw-semibold",
		"SELL": "text-danger fw-semibold", "REDUCE": "text-danger fw-semibold",
		"HOLD": "text-body-secondary", "WAIT": "text-body-secondary",
	}
	for action, want := range tests {
		if got := actionClass(action); got != want {
			t.Fatalf("actionClass(%q) = %q, want %q", action, got, want)
		}
	}
}

func TestRenderRecommendationsPageFallsBackToTextInputWithoutSymbols(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "recommendations", RecommendationsPage{Symbol: "AAPL"})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `name="symbol"`) || strings.Contains(rec.Body.String(), "<select") {
		t.Fatalf("expected a plain text input (no dropdown) when Symbols is empty, got %s", rec.Body.String())
	}
}

func TestRenderAlertsPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "alerts", AlertsPage{
		Alerts: []alerts.Alert{{
			Recommendation: decision.Recommendation{Symbol: "AAPL", Action: "REDUCE", Confidence: 0.65, Reason: []string{"Momentum signal is softening"}},
			TriggeredAt:    time.Now().Format(time.RFC3339),
		}},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "REDUCE") {
		t.Fatalf("expected REDUCE alert in rendered alerts page, got %s", rec.Body.String())
	}
}

func TestRenderBacktestsPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "backtests", BacktestsPage{
		Symbol: "AAPL", Window: 180, Capital: 10000,
		Result: &backtestingDomain.StrategyResult{
			Symbol: "AAPL", StrategyName: "atlas-orchestrator", StrategyVersion: "v1",
			TotalReturn: 0.12, SharpeRatio: 1.1, TradeCount: 1,
			Trades:       []backtestingDomain.Trade{{Timestamp: time.Now(), Action: "BUY", Price: 100, Quantity: 10, Confidence: 0.78}},
			MLEvaluation: &backtestingDomain.MLEvaluation{SampleSize: 40, Accuracy: 0.6, ROCAUC: 0.55},
		},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "atlas-orchestrator") || !strings.Contains(body, "12.00%") {
		t.Fatalf("expected strategy name and formatted return in rendered backtests page, got %s", body)
	}
}

func TestRenderWatchlistPage(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "watchlist", WatchlistPage{Items: []watchlist.Item{{Symbol: "AAPL", AddedAt: "2026-01-01T00:00:00Z"}}})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "AAPL") {
		t.Fatalf("expected AAPL row in rendered watchlist page, got %s", rec.Body.String())
	}
}

// The account page has no login section and asks for no password: Atlas has no accounts.
func TestRenderAccountPageHasNoCredentials(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "account", AccountPage{HasBackup: true})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, `type="password"`) || strings.Contains(body, "2FA") || strings.Contains(body, "/logout") {
		t.Fatalf("expected no credential fields, 2FA or logout on the account page")
	}
	if !strings.Contains(body, `action="/dashboard/account/flush"`) || !strings.Contains(body, `action="/dashboard/account/restore"`) {
		t.Fatal("expected the restore and flush forms")
	}
}

func TestRenderReportsTemplateErrorsAsHTTP500(t *testing.T) {
	rec := httptest.NewRecorder()
	// Missing template name entirely — ExecuteTemplate returns an error for an unknown name.
	Render(rec, "does-not-exist", nil)
	if rec.Code != 500 {
		t.Fatalf("expected 500 for an unknown template name, got %d", rec.Code)
	}
}

func TestFormatActiveDuration(t *testing.T) {
	now := time.Date(2026, 8, 10, 18, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		since time.Time
		want  string
	}{
		{"days drop the minutes", now.Add(-50 * time.Hour), "2d 2h"},
		{"hours keep the minutes", now.Add(-(3*time.Hour + 20*time.Minute)), "3h 20m"},
		{"under an hour is minutes only", now.Add(-45 * time.Minute), "45m"},
		// Sub-minute reads as "<1m" rather than "0m": the poll interval is five minutes, so
		// claiming to-the-second freshness would overstate what the data can support.
		{"under a minute", now.Add(-20 * time.Second), "<1m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FormatActiveDuration(LangEN, tc.since, now).Label; got != tc.want {
				t.Fatalf("FormatActiveDuration = %q, want %q", got, tc.want)
			}
		})
	}

	// Seconds back the column's numeric sort, so it must be the real elapsed span rather than a
	// re-parse of the rounded label.
	if got := FormatActiveDuration(LangEN, now.Add(-90*time.Minute), now).Seconds; got != 5400 {
		t.Fatalf("Seconds = %d, want 5400", got)
	}

	// No recorded history and a clock-skewed future timestamp both render as blank rather than
	// as an invented duration.
	if got := FormatActiveDuration(LangEN, time.Time{}, now); got.Label != "" || got.Seconds != 0 {
		t.Fatalf("a zero timestamp must render blank, got %+v", got)
	}
	if got := FormatActiveDuration(LangEN, now.Add(time.Hour), now); got.Label != "" {
		t.Fatalf("a future timestamp must render blank, got %+v", got)
	}

	if got := FormatActiveDuration(LangPL, now.Add(-(3*time.Hour + 20*time.Minute)), now).Label; got != "3godz 20min" {
		t.Fatalf("Polish units: got %q", got)
	}
	if got := FormatActiveDuration(LangDE, now.Add(-(3*time.Hour + 20*time.Minute)), now).Label; got != "3Std 20Min" {
		t.Fatalf("German units: got %q", got)
	}
	if got := FormatActiveDuration(LangFR, now.Add(-(3*time.Hour + 20*time.Minute)), now).Label; got != "3h 20min" {
		t.Fatalf("French units: got %q", got)
	}
	if got := FormatActiveDuration(LangES, now.Add(-(3*time.Hour + 20*time.Minute)), now).Label; got != "3h 20min" {
		t.Fatalf("Spanish units: got %q", got)
	}
}

// Every symbol Atlas shows links to its quote page, and every table column worth ordering by is
// marked sortable. Both are easy to add to one template and forget in the next, so assert them
// against a page that renders both a positions and a transactions table.
func TestRenderPortfolioPageLinksSymbolsAndMarksSortableColumns(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "portfolio", PortfolioPage{
		Positions: []portfolio.Position{
			{Symbol: "PKN.WA", Quantity: 10, AverageCost: 60, CurrentPrice: 72},
		},
		Transactions: []portfolio.Transaction{
			{ID: "tx-1", Symbol: "PKN.WA", Type: "BUY", Quantity: 10, Price: 60, Timestamp: "2026-01-01T00:00:00Z"},
		},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// The exchange suffix has to survive into the URL — a bare "PKN" is a different instrument.
	if !strings.Contains(body, `href="https://finance.yahoo.com/quote/PKN.WA/"`) {
		t.Errorf("expected the symbol to link to its Yahoo Finance quote page, got %s", body)
	}
	if strings.Count(body, `class="atlas-symbol"`) != 2 {
		t.Errorf("expected both the position and the transaction symbol to be linked")
	}
	// Opening a new tab without rel=noopener hands the destination a handle on this window.
	if !strings.Contains(body, `rel="noopener noreferrer"`) {
		t.Errorf("expected external symbol links to carry rel=noopener noreferrer")
	}
	for _, want := range []string{`data-sort="text"`, `data-sort="number"`, `data-sort="date"`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %s among the sortable column headers", want)
		}
	}
}

// The Alerts page carries the duration column, which needs both renderings present: the label a
// person reads and the seconds the column sorts on.
func TestRenderAlertsPageShowsActiveDuration(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "alerts", AlertsPage{
		Alerts: []alerts.Alert{{
			Recommendation: decision.Recommendation{Symbol: "ORCL", Action: "BUY", Confidence: 0.67},
			TriggeredAt:    time.Now().Format(time.RFC3339),
		}},
		ActiveFor: map[string]ActiveDuration{"ORCL": {Label: "3h 20m", Seconds: 12000}},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "3h 20m") {
		t.Errorf("expected the duration label in the rendered alerts page, got %s", body)
	}
	if !strings.Contains(body, `data-sort-value="12000"`) {
		t.Errorf("expected the sortable seconds value alongside the label, got %s", body)
	}
}

// A symbol with no recorded streak yet must render an empty cell rather than a zero duration,
// which would claim the alert had just appeared.
func TestRenderAlertsPageLeavesUnknownDurationBlank(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "alerts", AlertsPage{
		Alerts: []alerts.Alert{{
			Recommendation: decision.Recommendation{Symbol: "ORCL", Action: "BUY"},
			TriggeredAt:    time.Now().Format(time.RFC3339),
		}},
	})
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `data-sort-value="0"></td>`) {
		t.Errorf("expected a blank duration cell for a symbol with no streak, got %s", rec.Body.String())
	}
}

// A rejected submission re-renders with what was typed — key included, so resubmitting the
// corrected form still records at most one transaction.
func TestRenderPortfolioPageKeepsARejectedForm(t *testing.T) {
	rec := httptest.NewRecorder()
	Render(rec, "portfolio", PortfolioPage{
		Currencies: []string{"PLN", "EUR", "USD"},
		Error:      "Quantity and price must both be numbers.",
		Form: TransactionForm{
			Key: "0123456789abcdef0123456789abcdef", Symbol: "AAPL", Type: "SELL",
			Quantity: "3", Price: "abc", Currency: "EUR", Date: "2026-03-02", Today: "2026-09-24",
		},
	})
	body := rec.Body.String()
	for _, want := range []string{
		`name="key" value="0123456789abcdef0123456789abcdef"`,
		`id="txSymbol" name="symbol" value="AAPL"`,
		`<option value="SELL" selected>`,
		`name="quantity" value="3"`,
		`name="price" value="abc"`,
		`<option value="EUR" selected>`,
		`name="date" value="2026-03-02" max="2026-09-24"`,
		`<label class="form-label" for="txQuantity">`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected %q in the re-rendered form", want)
		}
	}
}
