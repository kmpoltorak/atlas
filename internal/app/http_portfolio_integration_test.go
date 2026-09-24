package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	portfolioUC "atlas/internal/application/portfolio"
	"atlas/internal/config"
	"atlas/internal/domain/portfolio"
	"atlas/internal/infrastructure/persistence"
	"atlas/internal/observability"

	"go.uber.org/zap"
)

// These drive real HTTP requests with real cookies against a real database — the boundary the
// review found the portfolio-selection bug at, and the one nothing was testing. A unit test on
// the use case could not have caught it: the bug was that the *request* did not carry its
// portfolio choice, not that the use case mishandled one.
func newHTTPTestApplication(t *testing.T) (*Application, http.Handler) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN not set; skipping HTTP portfolio integration test")
	}
	storage, err := persistence.NewTimescaleDB(context.Background(), dsn, persistence.PoolLimits{})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	uc, err := portfolioUC.NewUseCaseWithRepository(storage)
	if err != nil {
		t.Fatalf("portfolio use case: %v", err)
	}
	app := &Application{
		Config:    config.Config{},
		Metrics:   observability.NewMetrics(),
		Logger:    zap.NewNop(),
		Storage:   storage,
		Portfolio: uc,
		Leader:    newLeader(),
	}
	return app, app.NewHandler()
}

// twoPortfolios returns the ids of two distinct portfolios, creating a second if the instance
// only has the auto-created one.
//
// Anything it creates, it removes again. These tests run against a real database — the developer's
// own, in practice — and a helper that leaves a portfolio named "http-test-xx" sitting in the
// dashboard forever is the test suite quietly editing live data. Only portfolios this call
// created are removed; a pre-existing second portfolio is left alone.
func twoPortfolios(t *testing.T, app *Application) (string, string) {
	t.Helper()
	existing := app.Portfolio.PortfolioList()
	for len(existing) < 2 {
		created, err := app.Portfolio.CreatePortfolio("http-test-" + strings.Repeat("x", len(existing)+1))
		if err != nil {
			t.Fatalf("create portfolio: %v", err)
		}
		t.Cleanup(func() {
			if err := app.Portfolio.DeletePortfolio(created.ID); err != nil {
				t.Errorf("remove the portfolio this test created (%s): %v", created.Name, err)
			}
		})
		existing = append(existing, created)
	}
	return existing[0].ID, existing[1].ID
}

// postTransaction sends one API write carrying the portfolio cookie, the way a browser tab does.
func postTransaction(t *testing.T, handler http.Handler, portfolioID, symbol string, quantity float64) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(portfolio.Transaction{
		Symbol: symbol, Type: "BUY", Quantity: quantity, Price: 10, PriceInPLN: 10,
		Timestamp: "2026-09-05T09:00:00Z",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/portfolio", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin") // satisfies CrossOriginProtection
	request.AddCookie(&http.Cookie{Name: portfolioCookieName, Value: portfolioID})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// The original finding: a request's portfolio came from a process-global "active portfolio", so
// two concurrent requests naming different portfolios could each land in the other's.
func TestConcurrentRequestsWriteToTheirOwnPortfolio(t *testing.T) {
	app, handler := newHTTPTestApplication(t)
	first, second := twoPortfolios(t, app)

	const rounds = 12
	var wg sync.WaitGroup
	for i := 0; i < rounds; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if got := postTransaction(t, handler, first, "TESTHTTPA", 1); got.Code != http.StatusCreated && got.Code != http.StatusOK {
				t.Errorf("write to the first portfolio failed: %d %s", got.Code, got.Body)
			}
		}()
		go func() {
			defer wg.Done()
			if got := postTransaction(t, handler, second, "TESTHTTPB", 1); got.Code != http.StatusCreated && got.Code != http.StatusOK {
				t.Errorf("write to the second portfolio failed: %d %s", got.Code, got.Body)
			}
		}()
	}
	wg.Wait()

	t.Cleanup(func() { removeTestLedger(app, []string{first, second}, "TESTHTTPA", "TESTHTTPB") })

	firstPositions := positionsOf(t, app, first)
	secondPositions := positionsOf(t, app, second)

	if quantityOf(firstPositions, "TESTHTTPB") != 0 {
		t.Fatalf("a symbol written to the second portfolio leaked into the first: %+v", firstPositions)
	}
	if quantityOf(secondPositions, "TESTHTTPA") != 0 {
		t.Fatalf("a symbol written to the first portfolio leaked into the second: %+v", secondPositions)
	}
	if got := quantityOf(firstPositions, "TESTHTTPA"); got != rounds {
		t.Fatalf("expected %d shares in the first portfolio, got %v", rounds, got)
	}
	if got := quantityOf(secondPositions, "TESTHTTPB"); got != rounds {
		t.Fatalf("expected %d shares in the second portfolio, got %v", rounds, got)
	}
}

// A request with no cookie must not inherit whatever portfolio some other request last selected.
func TestARequestWithoutACookieUsesTheFirstPortfolioNotTheLastSelected(t *testing.T) {
	app, handler := newHTTPTestApplication(t)
	first, second := twoPortfolios(t, app)

	// A request that explicitly selects the second portfolio.
	postTransaction(t, handler, second, "TESTHTTPC", 1)
	t.Cleanup(func() { removeTestLedger(app, []string{first, second}, "TESTHTTPC", "TESTHTTPD") })

	// Then one with no cookie at all: it must fall back to the first portfolio deterministically.
	request := httptest.NewRequest(http.MethodGet, "/portfolio", nil)
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /portfolio without a cookie failed: %d %s", recorder.Code, recorder.Body)
	}

	var payload struct {
		Positions []portfolio.Position `json:"positions"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if quantityOf(payload.Positions, "TESTHTTPC") != 0 {
		t.Fatal("a cookieless request saw the portfolio another request had selected")
	}
}

// Validation has to hold at the HTTP boundary too, not only in the use case — the API accepts a
// JSON body directly, with no form parsing in between to reject anything first.
func TestTheAPIRejectsImpossibleTransactions(t *testing.T) {
	_, handler := newHTTPTestApplication(t)

	for name, body := range map[string]string{
		"negative quantity": `{"symbol":"TESTHTTPX","type":"BUY","quantity":-5,"price":10}`,
		"zero quantity":     `{"symbol":"TESTHTTPX","type":"BUY","quantity":0,"price":10}`,
		"NaN quantity":      `{"symbol":"TESTHTTPX","type":"BUY","quantity":1e999,"price":10}`,
		"negative price":    `{"symbol":"TESTHTTPX","type":"BUY","quantity":1,"price":-10}`,
		"unknown type":      `{"symbol":"TESTHTTPX","type":"SHORT","quantity":1,"price":10}`,
		"missing symbol":    `{"symbol":"","type":"BUY","quantity":1,"price":10}`,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/portfolio", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code < 400 {
				t.Fatalf("the API accepted a transaction with a %s: %d %s", name, recorder.Code, recorder.Body)
			}
		})
	}
}

func positionsOf(t *testing.T, app *Application, portfolioID string) []portfolio.Position {
	t.Helper()
	uc, err := app.Portfolio.ForPortfolio(portfolioID, nil)
	if err != nil {
		t.Fatalf("scope to portfolio: %v", err)
	}
	positions, err := uc.GetPositions()
	if err != nil {
		t.Fatalf("read positions: %v", err)
	}
	return positions
}

func quantityOf(positions []portfolio.Position, symbol string) float64 {
	for _, position := range positions {
		if position.Symbol == symbol {
			return position.Quantity
		}
	}
	return 0
}

// removeTestLedger deletes the given symbols' transactions from each portfolio. Removing only the
// positions is not a cleanup: the ledger survives, and the next run's first trade on the symbol
// replays it, so a rerun against the same database counted every earlier run's shares too.
func removeTestLedger(app *Application, portfolioIDs []string, symbols ...string) {
	wanted := make(map[string]bool, len(symbols))
	for _, symbol := range symbols {
		wanted[symbol] = true
	}
	for _, id := range portfolioIDs {
		uc, err := app.Portfolio.ForPortfolio(id, nil)
		if err != nil {
			continue
		}
		transactions, err := uc.GetTransactions()
		if err != nil {
			continue
		}
		var ids []string
		for _, tx := range transactions {
			if wanted[tx.Symbol] {
				ids = append(ids, tx.ID)
			}
		}
		if len(ids) > 0 {
			_ = uc.RemoveTransactions(ids)
		}
	}
}
