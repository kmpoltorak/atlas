package app

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	watchlistUC "atlas/internal/application/watchlist"
	"atlas/internal/domain/currency"
	"atlas/internal/domain/portfolio"
)

// oneDayRates knows a single historical USD rate and a different one for today, so a test can
// tell which of the two a conversion used.
type oneDayRates struct {
	date  string
	rate  float64
	today float64
}

func (o oneDayRates) ToBase(amount float64, _ currency.Code) (float64, error) {
	return amount * o.today, nil
}

func (o oneDayRates) ToBaseOn(amount float64, _ currency.Code, date time.Time) (float64, bool, error) {
	if date.Format("2006-01-02") == o.date {
		return amount * o.rate, true, nil
	}
	return amount * o.today, false, nil
}

func formTransactions(t *testing.T, app *Application, symbol string) []portfolio.Transaction {
	t.Helper()
	uc, err := app.Portfolio.ForPortfolio("", nil)
	if err != nil {
		t.Fatalf("portfolio: %v", err)
	}
	all, err := uc.GetTransactions()
	if err != nil {
		t.Fatalf("transactions: %v", err)
	}
	var matching []portfolio.Transaction
	for _, tx := range all {
		if tx.Symbol == symbol {
			matching = append(matching, tx)
		}
	}
	return matching
}

func removeFormTransactions(t *testing.T, app *Application, symbol string) {
	t.Helper()
	uc, err := app.Portfolio.ForPortfolio("", nil)
	if err != nil {
		return
	}
	for _, tx := range formTransactions(t, app, symbol) {
		_ = uc.RemoveTransactions([]string{tx.ID})
	}
}

// The dashboard form had no date, so every entry was stamped now and a USD/EUR purchase made
// months ago was converted at today's rate. And each submission minted a fresh id, so a double
// click or a retry after a dropped connection recorded the trade twice.
func TestTransactionFormRecordsTheTradeDateOnceAcrossResubmits(t *testing.T) {
	app, _ := newHTTPTestApplication(t)
	app.Portfolio.ExchangeRates = oneDayRates{date: "2026-03-10", rate: 3.90, today: 4.50}
	handler := app.NewHandler()
	t.Cleanup(func() { removeFormTransactions(t, app, "TESTFORMDATE") })

	form := url.Values{
		"key": {"5f1d2c3b4a5968778695a4b3c2d1e0f9"}, "symbol": {"TESTFORMDATE"}, "type": {"BUY"},
		"quantity": {"2"}, "price": {"100"}, "currency": {"USD"}, "date": {"2026-03-10"},
	}
	for attempt := 0; attempt < 2; attempt++ {
		got := sendForm(handler, http.MethodPost, "/dashboard/portfolio", "", form)
		if got.Code != http.StatusSeeOther {
			t.Fatalf("submission %d: expected a redirect after recording, got %d: %s", attempt+1, got.Code, got.Body)
		}
	}

	recorded := formTransactions(t, app, "TESTFORMDATE")
	if len(recorded) != 1 {
		t.Fatalf("two submissions of one form must record one transaction, got %d", len(recorded))
	}
	if recorded[0].PriceInPLN != 390 {
		t.Fatalf("expected the 2026-03-10 rate (3.90 → 390 PLN), got %v", recorded[0].PriceInPLN)
	}
	if day := recorded[0].Timestamp[:10]; day != "2026-03-10" {
		t.Fatalf("expected the trade date to be kept, got %s", recorded[0].Timestamp)
	}
}

// A form without its key is refused rather than recorded without duplicate protection.
func TestTransactionFormWithoutAKeyIsRefused(t *testing.T) {
	app, _ := newHTTPTestApplication(t)
	app.Watchlist = watchlistUC.NewUseCase() // the rejection re-renders the page, which lists it
	handler := app.NewHandler()
	t.Cleanup(func() { removeFormTransactions(t, app, "TESTFORMNOKEY") })
	got := sendForm(handler, http.MethodPost, "/dashboard/portfolio", "", url.Values{
		"symbol": {"TESTFORMNOKEY"}, "type": {"BUY"}, "quantity": {"1"}, "price": {"10"}, "currency": {"PLN"},
	})
	if got.Code == http.StatusSeeOther {
		t.Fatal("a keyless submission was recorded")
	}
	if n := len(formTransactions(t, app, "TESTFORMNOKEY")); n != 0 {
		t.Fatalf("expected nothing recorded, got %d", n)
	}
}
