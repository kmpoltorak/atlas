package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	analyzerUC "atlas/internal/application/analyzer"
	collectorUC "atlas/internal/application/collector"
	decisionUC "atlas/internal/application/decision"
	"atlas/internal/domain/market"
)

// gatedProvider holds FetchBars until released, so a test can let the HTTP request finish first.
type gatedProvider struct {
	loopStubProvider
	release chan struct{}
}

func (p gatedProvider) FetchBars(ctx context.Context, symbol string, window int) ([]market.Bar, error) {
	<-p.release
	return p.loopStubProvider.FetchBars(ctx, symbol, window)
}

// The refresh a transaction triggers runs after the response is written, on the request-scoped
// Application. Its bar fetch had its own context, but the writes went through a.Storage, still
// bound to the finished request, and failed with "context canceled". This finishes the request,
// only then lets the provider answer, and checks the recommendation actually reaches the database.
func TestRecommendationRefreshOutlivesTheRequestThatTriggeredIt(t *testing.T) {
	app, _ := newHTTPTestApplication(t)
	release := make(chan struct{})
	app.Collector = &collectorUC.UseCase{Provider: gatedProvider{release: release}}
	app.Analyzer = analyzerUC.NewUseCase()
	app.Orchestrator = decisionUC.NewOrchestrator(app.Analyzer, app.Portfolio, decisionUC.NewUseCase())
	app.Config.CollectorWindow = 30
	handler := app.NewHandler()

	started := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	body := `{"symbol":"TESTREFRESH","type":"BUY","quantity":1,"price":10,"price_in_pln":10,"timestamp":"2026-09-05T09:00:00Z"}`
	request := httptest.NewRequest(http.MethodPost, "/portfolio", strings.NewReader(body)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	cancel() // the request is over, exactly as net/http cancels it after the handler returns
	if recorder.Code != http.StatusCreated {
		t.Fatalf("record transaction: %d %s", recorder.Code, recorder.Body)
	}
	t.Cleanup(func() {
		uc, err := app.Portfolio.ForPortfolio("", nil)
		if err != nil {
			return
		}
		transactions, _ := uc.GetTransactions()
		for _, tx := range transactions {
			if tx.Symbol == "TESTREFRESH" {
				_ = uc.RemoveTransactions([]string{tx.ID})
			}
		}
	})

	close(release)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		latest, err := app.Storage.LatestRecommendations()
		if err != nil {
			t.Fatalf("read recommendations: %v", err)
		}
		// Only a row written by this refresh counts, not one an earlier run left behind.
		if rec, ok := latest["TESTREFRESH"]; ok && !rec.CreatedAt.Before(started) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("the refreshed recommendation never reached the database")
}
