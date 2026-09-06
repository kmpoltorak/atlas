package app

import (
	"context"
	"net/http"
	"net/http/httptest"
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

// The 2026-09-06 verification's finding 4: admission bounded the simulations *running*, but a
// client that stayed connected simply waited, and nothing bounded how many could. r.Context()
// carries no deadline of its own and the server's WriteTimeout does not supply one, so each extra
// request parked a goroutine holding the bars it had already collected.
//
// Both entry points share one Admission, so both are driven here — a saturated instance must
// answer, not accumulate.
func TestBacktestEntryPointsRefuseWhenSaturated(t *testing.T) {
	app := newOverloadedBacktestApplication(t)
	handler := app.NewHandler()

	for _, entry := range []struct {
		name       string
		path       string
		retryAfter string
	}{
		{"single run", "/backtests?symbol=AAPL", "10"},
		{"walk forward", "/backtests/walkforward?symbol=AAPL", "30"},
	} {
		t.Run(entry.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, entry.path, nil)
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			recorder := httptest.NewRecorder()

			// A client that stays connected for the whole exchange: no cancellation, no deadline.
			// Before the queue bound this never returned.
			done := make(chan struct{})
			go func() {
				defer close(done)
				handler.ServeHTTP(recorder, request)
			}()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("a saturated instance left a connected client waiting instead of answering")
			}

			if recorder.Code != http.StatusServiceUnavailable {
				t.Fatalf("expected 503 while saturated, got %d", recorder.Code)
			}
			if got := recorder.Header().Get("Retry-After"); got != entry.retryAfter {
				t.Fatalf("expected Retry-After %s, got %q", entry.retryAfter, got)
			}
		})
	}
}

// newOverloadedBacktestApplication returns an Application whose backtest admission is already
// full — one simulation running and every queue place taken — with the occupants released when
// the test ends.
func newOverloadedBacktestApplication(t *testing.T) *Application {
	t.Helper()
	orchestrator := decisionUC.NewOrchestrator(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), decisionUC.NewUseCase())
	backtesting := backtestingUC.NewUseCase(orchestrator)
	backtesting.Admission = backtestingUC.NewAdmission(1)

	release, err := backtesting.Admission.Acquire(context.Background())
	if err != nil {
		t.Fatalf("occupy the only slot: %v", err)
	}
	t.Cleanup(release)

	// Fill every queue place. These are cancelled on cleanup rather than left to wait out the
	// queue deadline, so the test does not leave goroutines behind.
	capacity := backtesting.Admission.QueueCapacity()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	for i := 0; i < capacity; i++ {
		go func() { _, _ = backtesting.Admission.Acquire(ctx) }()
	}
	deadline := time.Now().Add(2 * time.Second)
	for backtesting.Admission.Queued() < capacity && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if queued := backtesting.Admission.Queued(); queued < capacity {
		t.Fatalf("the queue never filled: %d of %d places taken", queued, capacity)
	}

	return &Application{
		Config:      config.Config{},
		Metrics:     observability.NewMetrics(),
		Logger:      zap.NewNop(),
		Collector:   &collectorUC.UseCase{Provider: loopStubProvider{}},
		Backtesting: backtesting,
		Leader:      newLeader(),
	}
}
