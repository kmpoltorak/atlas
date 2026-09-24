package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	analyzerUC "atlas/internal/application/analyzer"
	decisionUC "atlas/internal/application/decision"
	portfolioUC "atlas/internal/application/portfolio"
	"atlas/internal/config"
	"atlas/internal/infrastructure/persistence"
	"atlas/internal/observability"

	"go.uber.org/zap"
)

// The 2026-09-07 review's fifth finding: creating a portfolio goes through a request-scoped view
// and writes to the database, but the Account page it redirects to rendered PortfolioList() off
// the shared use case — a snapshot loaded at startup. The portfolio the operator had just created
// was therefore missing from the page confirming they had created it.
//
// Driven over real HTTP against a real database, because that is where the staleness lives: the
// use case is not wrong, the page was reading the wrong instance of it.
func TestAccountPageShowsAPortfolioCreatedThroughTheDashboard(t *testing.T) {
	app, handler := newAccountHTTPTestApplication(t)

	// A previous crashed run can leave its portfolio behind, and the limit is five — clear any
	// leftovers of this test's own before adding another.
	for _, item := range app.Portfolio.PortfolioList() {
		if strings.HasPrefix(item.Name, "acct-test-") {
			if err := app.Portfolio.DeletePortfolio(item.ID); err != nil {
				t.Fatalf("clear a leftover test portfolio: %v", err)
			}
		}
	}

	name := "acct-test-" + time.Now().UTC().Format("150405.000")
	form := strings.NewReader("name=" + name)
	create := httptest.NewRequest(http.MethodPost, "/dashboard/portfolios/create", form)
	create.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	create.Header.Set("Sec-Fetch-Site", "same-origin")
	createRecorder := httptest.NewRecorder()
	handler.ServeHTTP(createRecorder, create)
	if createRecorder.Code != http.StatusSeeOther {
		t.Fatalf("expected the create form to redirect, got %d: %s", createRecorder.Code, createRecorder.Body.String())
	}
	t.Cleanup(func() {
		for _, item := range app.Portfolio.PortfolioList() {
			if item.Name == name {
				if err := app.Portfolio.DeletePortfolio(item.ID); err != nil {
					t.Errorf("remove the portfolio this test created: %v", err)
				}
			}
		}
	})

	account := httptest.NewRequest(http.MethodGet, "/dashboard/account", nil)
	account.Header.Set("Sec-Fetch-Site", "same-origin")
	accountRecorder := httptest.NewRecorder()
	handler.ServeHTTP(accountRecorder, account)

	if accountRecorder.Code != http.StatusOK {
		t.Fatalf("expected the account page to render, got %d", accountRecorder.Code)
	}
	if !strings.Contains(accountRecorder.Body.String(), name) {
		t.Fatalf("the account page does not list the portfolio just created (%s)", name)
	}
}

func newAccountHTTPTestApplication(t *testing.T) (*Application, http.Handler) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN not set; skipping account page integration test")
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
		// A real orchestrator: the page reads its ML toggle. ML itself stays nil, which is the
		// normal "no model configured" state the page has to render anyway.
		Orchestrator: decisionUC.NewOrchestrator(analyzerUC.NewUseCase(), uc, decisionUC.NewUseCase()),
		Leader:       newLeader(),
	}
	return app, app.NewHandler()
}
