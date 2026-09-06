package backtesting

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Cancellation stops an abandoned experiment; it does not stop ten of them starting. This is the
// bound the original review asked for and the follow-up found still missing.
func TestAdmissionBoundsConcurrentSimulations(t *testing.T) {
	admission := NewAdmission(2)

	var inFlight, peak, admitted int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := admission.Acquire(context.Background())
			// Eight requests against two slots and a bounded queue: some are turned away, which is
			// the point of the bound. What must hold regardless is that nothing exceeds the limit.
			if errors.Is(err, ErrBusy) {
				return
			}
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			atomic.AddInt32(&admitted, 1)
			defer release()
			current := atomic.AddInt32(&inFlight, 1)
			for {
				high := atomic.LoadInt32(&peak)
				if current <= high || atomic.CompareAndSwapInt32(&peak, high, current) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
		}()
	}
	wg.Wait()

	if peak > 2 {
		t.Fatalf("admission allowed %d concurrent simulations against a limit of 2", peak)
	}
	if admitted == 0 {
		t.Fatal("admission refused everything; the bound has become a blanket rejection")
	}
	if admission.InFlight() != 0 {
		t.Fatalf("every slot must be released, %d still held", admission.InFlight())
	}
}

// A client that gives up while queued must stop occupying the queue — otherwise a saturated
// instance stays saturated with work nobody is waiting for.
func TestAcquireGivesUpWhenTheCallerDoes(t *testing.T) {
	admission := NewAdmission(1)
	release, err := admission.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := admission.Acquire(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled caller must not keep waiting, got %v", err)
	}
}

// A caller that waited out its deadline gets ErrBusy, which the handler turns into 503 —
// distinct from "the client disconnected", which is nobody's error.
func TestADeadlineWhileQueuedReportsBusy(t *testing.T) {
	admission := NewAdmission(1)
	release, err := admission.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := admission.Acquire(ctx); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy after waiting out the deadline, got %v", err)
	}
}

// A nil Admission means unbounded, so existing callers and tests keep working unchanged.
func TestNilAdmissionIsUnbounded(t *testing.T) {
	var admission *Admission
	release, err := admission.Acquire(context.Background())
	if err != nil {
		t.Fatalf("a nil admission must admit everything, got %v", err)
	}
	release()
}

// The default leaves a core for everything else the process has to do.
func TestDefaultSlotsLeaveHeadroom(t *testing.T) {
	if slots := DefaultAdmissionSlots(); slots < 1 {
		t.Fatalf("there must always be at least one slot, got %d", slots)
	}
}

// Walk-forward runs many simulations back to back and takes a slot too — and must not deadlock
// against itself when the inner runs reuse the same use case.
func TestWalkForwardDoesNotDeadlockAgainstASingleSlot(t *testing.T) {
	u := NewUseCase(nil)
	u.Admission = NewAdmission(1)

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Fails validation (no orchestrator), which is fine: what matters is that it returns
		// rather than blocking forever on its own slot.
		_, _ = u.RunWalkForward("AAPL", nil, 1000, "test", "v1")
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("walk-forward deadlocked on its own admission slot")
	}
}

// Bounding the runners was only half of it. Every request beyond them still parked a goroutine
// holding whatever bars it had already collected, and nothing bounded how many of those there
// could be or how long each waited — r.Context() carries no deadline for a client that stays
// connected, and the server's WriteTimeout does not put one there. So a saturated server grew
// memory instead of saying it was saturated.
func TestAdmissionRefusesRatherThanQueueingWithoutLimit(t *testing.T) {
	admission := NewAdmission(1)
	admission.wait = 50 * time.Millisecond // keep the test's own wait short; the bound is what matters

	// Occupy the only slot.
	release, err := admission.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release()

	// Fill the queue. These wait, because a slot really might free up in a moment.
	queued := make(chan error, defaultQueueDepth)
	var waiting sync.WaitGroup
	for i := 0; i < defaultQueueDepth; i++ {
		waiting.Add(1)
		go func() {
			defer waiting.Done()
			_, err := admission.Acquire(context.Background())
			queued <- err
		}()
	}
	// Let them reach the queue before measuring it.
	deadline := time.Now().Add(2 * time.Second)
	for admission.Queued() < defaultQueueDepth && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if admission.Queued() != defaultQueueDepth {
		t.Fatalf("expected %d waiters, got %d", defaultQueueDepth, admission.Queued())
	}

	// One more, with the client still connected and no deadline of its own. Before the bound this
	// waited forever; now it is told immediately.
	start := time.Now()
	if _, err := admission.Acquire(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("a request beyond the queue bound must be refused as busy, got %v", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("the refusal must be immediate, not after a wait; took %v", elapsed)
	}

	waiting.Wait()
	close(queued)
	for err := range queued {
		if !errors.Is(err, ErrBusy) {
			t.Fatalf("a queued request that waited out its deadline must be told the server is busy, got %v", err)
		}
	}
}

// A connected client with no deadline of its own must not wait indefinitely. This is the half of
// the bound that the queue depth alone does not cover.
func TestAQueuedRequestGivesUpAfterTheQueueWait(t *testing.T) {
	admission := NewAdmission(1)
	admission.wait = 100 * time.Millisecond

	release, err := admission.Acquire(context.Background())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer release()

	start := time.Now()
	if _, err := admission.Acquire(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy after the queue wait, got %v", err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("the wait was skipped entirely; took %v", elapsed)
	}
}

// The default has to describe the container's share of the machine, not the host's. On a 16-core
// host with a 2-core limit, NumCPU admitted 15 simulations onto 2 cores.
func TestDefaultAdmissionSlotsFollowsTheContainersCPUShare(t *testing.T) {
	previous := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(previous)

	if slots := DefaultAdmissionSlots(); slots != 1 {
		t.Fatalf("with two usable cores one should be left for everything else, got %d slots", slots)
	}
}
