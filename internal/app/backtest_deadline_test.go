package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	analyzerUC "atlas/internal/application/analyzer"
	backtestingUC "atlas/internal/application/backtesting"
	collectorUC "atlas/internal/application/collector"
	decisionUC "atlas/internal/application/decision"
	portfolioUC "atlas/internal/application/portfolio"
	"atlas/internal/config"
	"atlas/internal/observability"

	"go.uber.org/zap"
)

// The 2026-09-06 verification's finding 5: admission may hold a request for up to 30s waiting for
// a slot while the server's WriteTimeout is 10s, so a request could finish queuing, run its
// simulation, and only then find it could no longer write the answer — and nothing would have
// cancelled the simulation, because WriteTimeout does not touch the request context.
//
// A recorder cannot show this: httptest.NewRecorder enforces no deadline at all. This runs a real
// server with a write timeout deliberately shorter than the queue wait it induces.
func TestBacktestSurvivesAQueueWaitLongerThanTheWriteTimeout(t *testing.T) {
	const writeTimeout = 250 * time.Millisecond

	app, releaseSlot := newQueuedBacktestApplication(t)
	server := httptest.NewUnstartedServer(app.NewHandler())
	server.Config.WriteTimeout = writeTimeout
	server.Start()
	defer server.Close()

	// Free the only slot after the write deadline would already have expired, so the request is
	// forced to answer from beyond it.
	time.AfterFunc(3*writeTimeout, releaseSlot)

	request, err := http.NewRequest(http.MethodGet, server.URL+"/backtests?symbol=AAPL&window=30", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Sec-Fetch-Site", "same-origin")

	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("the server dropped the connection instead of answering past its write deadline: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("the response was cut off mid-write: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected the queued backtest to run, got %d: %s", response.StatusCode, body)
	}
	if len(body) == 0 {
		t.Fatal("expected a backtest result, got an empty body")
	}
}

// newQueuedBacktestApplication returns an Application whose only simulation slot is already
// taken, plus the function that frees it. Anything the handler does then has to wait in the
// admission queue first.
func newQueuedBacktestApplication(t *testing.T) (*Application, func()) {
	t.Helper()
	orchestrator := decisionUC.NewOrchestrator(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), decisionUC.NewUseCase())
	backtesting := backtestingUC.NewUseCase(orchestrator)
	backtesting.Admission = backtestingUC.NewAdmission(1)

	release, err := backtesting.Admission.Acquire(context.Background())
	if err != nil {
		t.Fatalf("occupy the only slot: %v", err)
	}
	// sync.Once because the timer and t.Cleanup both call it, from different goroutines.
	var once sync.Once
	releaseOnce := func() { once.Do(release) }
	t.Cleanup(releaseOnce)

	return &Application{
		Config:      config.Config{},
		Metrics:     observability.NewMetrics(),
		Logger:      zap.NewNop(),
		Collector:   &collectorUC.UseCase{Provider: loopStubProvider{}},
		Backtesting: backtesting,
		Leader:      newLeader(),
	}, releaseOnce
}
