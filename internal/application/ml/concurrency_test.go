package ml

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	domainml "atlas/internal/domain/ml"

	ort "github.com/yalue/onnxruntime_go"
)

// countingSession stands in for a real onnxruntime session. The interesting part is Destroy:
// a real one frees native memory, so a Run against a destroyed session is not a Go error but a
// process-level crash. Here it is a flag, which turns the same bug into a test failure.
type countingSession struct {
	destroyed atomic.Bool
	runs      atomic.Int64
	t         *testing.T
}

func (s *countingSession) Run(inputs, outputs []ort.Value) error {
	if s.destroyed.Load() {
		// In production this is where the process would die inside the native library, not
		// return an error — which is why the guard has to be in Go, before this point.
		s.t.Error("inference ran against a destroyed session")
	}
	s.runs.Add(1)
	return nil
}

func (s *countingSession) Destroy() error {
	s.destroyed.Store(true)
	return nil
}

func testModel(t *testing.T, version string) (*OnnxModel, *countingSession) {
	session := &countingSession{t: t}
	return &OnnxModel{session: session, Version: version}, session
}

// Swapping the active model while predictions are in flight must never free a session another
// goroutine is still inside. An atomic pointer swap alone does not give that: it makes the
// pointer read safe, and says nothing about the native session's lifetime.
//
// Run with -race, this also covers the plain data race on the model pointer.
func TestActivatingAModelDoesNotFreeOneInUse(t *testing.T) {
	uc := NewUseCase()
	first, _ := testModel(t, "v1")
	uc.SetModel(first)

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for i := 0; i < 8; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					// An error here is fine — a closed model reports one, and without the native
					// runtime loaded tensor allocation fails too. Reaching session.Run after
					// Destroy is what must never happen, and countingSession fails the test if
					// it does. Under -race this also covers the model pointer itself.
					_, _ = uc.Predict(domainml.FeatureVector{RSI: 50, Volume: 1})
				}
			}
		}()
	}

	// Meanwhile, keep swapping and closing the previous model — the Settings page's activation
	// flow, run in a tight loop.
	for i := 0; i < 30; i++ {
		next, _ := testModel(t, "v-next")
		if previous := uc.SetModel(next); previous != nil {
			if err := previous.Close(); err != nil {
				t.Errorf("closing the previous model failed: %v", err)
			}
		}
	}
	close(stop)
	readers.Wait()

	if final := uc.CurrentModel(); final != nil {
		_ = final.Close()
	}
}

// Close is reachable from more than one path (activation, shutdown), so it has to be safe to
// call twice — a double free in the native library is a crash, not an error.
func TestClosingAModelTwiceIsSafe(t *testing.T) {
	model, session := testModel(t, "v1")

	if err := model.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := model.Close(); err != nil {
		t.Fatalf("second close must be a no-op, got %v", err)
	}
	if !session.destroyed.Load() {
		t.Fatal("the session should have been destroyed")
	}
}

// A closed model must report an error rather than reaching into freed memory. The guard has to
// sit in Go, before any tensor is built: by the time the native library is handed a destroyed
// session there is no error to return, only a crash.
func TestPredictOnAClosedModelErrors(t *testing.T) {
	model, session := testModel(t, "v1")
	_ = model.Close()

	_, err := model.PredictCorrectionProbability(domainml.FeatureVector{RSI: 50})
	if err == nil {
		t.Fatal("expected a closed model to refuse inference")
	}
	if err.Error() != "model is closed" {
		t.Fatalf("expected the closed-model guard to fire first, got %v", err)
	}
	if session.runs.Load() != 0 {
		t.Fatal("inference reached the destroyed session")
	}
}

// Close racing inference must always resolve one way or the other, never into a session that is
// being freed. Run with -race this is also the data-race check on the closed flag.
func TestClosingWhilePredictingNeverReachesADestroyedSession(t *testing.T) {
	for attempt := 0; attempt < 50; attempt++ {
		model, _ := testModel(t, "v1")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = model.PredictCorrectionProbability(domainml.FeatureVector{RSI: 50})
		}()
		go func() {
			defer wg.Done()
			_ = model.Close()
		}()
		wg.Wait()
	}
}

// A backtest pins the models it started with, so an activation partway through cannot change the
// experiment's inputs underneath it — and cannot free them either.
func TestSnapshotPinsModelsForTheDurationOfAnExperiment(t *testing.T) {
	uc := NewUseCase()
	first, firstSession := testModel(t, "v1")
	uc.SetModel(first)

	pinned, release := uc.Snapshot()
	if pinned.CurrentModel() != first {
		t.Fatal("the snapshot must hold the model that was active when it was taken")
	}

	swapped := make(chan struct{})
	go func() {
		next, _ := testModel(t, "v2")
		if previous := uc.SetModel(next); previous != nil {
			_ = previous.Close()
		}
		close(swapped)
	}()

	// The swap must not complete while the snapshot is held.
	select {
	case <-swapped:
		t.Fatal("a model activation completed while a snapshot was pinning it")
	case <-time.After(20 * time.Millisecond):
	}
	if firstSession.destroyed.Load() {
		t.Fatal("the pinned session was destroyed while still pinned")
	}

	release()
	<-swapped
}
