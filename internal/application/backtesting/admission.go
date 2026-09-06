package backtesting

import (
	"context"
	"errors"
	"runtime"
	"time"
)

// ErrBusy means every simulation slot is taken. It is a load signal, not a failure: the caller
// should tell the user to retry rather than dressing it up as a server error.
var ErrBusy = errors.New("too many backtests are already running; try again shortly")

// Admission bounds how many simulations run at once.
//
// Cancellation alone was not enough. A backtest replays the whole decision pipeline bar by bar,
// which is CPU-bound and takes seconds; a handler that starts one per request lets a page reload,
// or a script, put more simulations in flight than the container has cores. They then all run
// slower, the HTTP write deadlines start expiring, and the alert poll loop — sharing the same
// process — is starved by work nobody is waiting for any more.
//
// A plain buffered channel rather than golang.org/x/sync/semaphore: the whole mechanism is
// acquire, release, and give up when the caller's context ends, which is three lines.
type Admission struct {
	slots chan struct{}
	// queue bounds how many requests may be *waiting* for a slot. Bounding the runners alone was
	// not enough: every additional request still parked a goroutine holding the bars it had
	// already collected, so a client that kept its connection open could grow memory without ever
	// running a simulation. A waiter is cheap; unbounded waiters are not.
	queue chan struct{}
	// wait is how long a queued request waits before being told the server is busy. Without it the
	// only bound was the caller's own context, and neither r.Context() nor the server's
	// WriteTimeout supplies a deadline — a connected client would wait indefinitely.
	wait time.Duration
}

// DefaultAdmissionSlots leaves at least one core for everything else the process has to do —
// serving requests, polling the watchlist — rather than letting simulations take the machine.
//
// GOMAXPROCS, not NumCPU: NumCPU reports the host's cores, which in a container with a CPU limit
// is a number this process can never actually use — on a 16-core host with a 2-core limit it
// admitted 15 simulations onto 2 cores. Since Go 1.25 GOMAXPROCS is derived from the cgroup CPU
// limit, so this is the container's share, which is the number that matters.
func DefaultAdmissionSlots() int {
	if slots := runtime.GOMAXPROCS(0) - 1; slots > 0 {
		return slots
	}
	return 1
}

// defaultQueueDepth is how many requests may queue per slot. Two is enough to absorb a double-
// click or a page reload landing on top of a running simulation; beyond that the honest answer is
// that the server is busy, delivered immediately rather than after a long wait.
const defaultQueueDepth = 2

// defaultQueueWait bounds a queued request. Long enough that a simulation finishing frees the slot
// for the waiter, short enough to stay well inside any sane HTTP client timeout.
const defaultQueueWait = 30 * time.Second

func NewAdmission(slots int) *Admission {
	if slots < 1 {
		slots = 1
	}
	return &Admission{
		slots: make(chan struct{}, slots),
		queue: make(chan struct{}, slots*defaultQueueDepth),
		wait:  defaultQueueWait,
	}
}

// Acquire takes a slot, waiting until one frees up, the queue wait elapses, or ctx ends. It
// returns the release function; callers must defer it.
//
// Queuing rather than rejecting immediately is deliberate: a backtest is a deliberate, expensive
// action a person just asked for, and waiting a few seconds for a slot is a better answer than
// "busy" when one is about to free up. What is *not* deliberate is queuing without limit, so the
// queue is bounded in both directions — how many may wait, and for how long. Past either, the
// answer is ErrBusy, which the handlers turn into 503 with a Retry-After.
func (a *Admission) Acquire(ctx context.Context) (release func(), err error) {
	if a == nil {
		return func() {}, nil
	}
	// A place in the queue, taken without blocking: if there is none, the server is saturated and
	// saying so now is better than parking another goroutine to find out later.
	select {
	case a.queue <- struct{}{}:
		defer func() { <-a.queue }()
	default:
		return nil, ErrBusy
	}

	ctx, cancel := context.WithTimeout(ctx, a.wait)
	defer cancel()
	select {
	case a.slots <- struct{}{}:
		return func() { <-a.slots }, nil
	case <-ctx.Done():
		// Distinguish "the client went away" from "we are saturated": only the latter is worth
		// showing as a busy response.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrBusy
		}
		return nil, ctx.Err()
	}
}

// Queued reports how many requests are currently waiting for a slot — for tests and the busy
// message, alongside InFlight.
func (a *Admission) Queued() int {
	if a == nil {
		return 0
	}
	return len(a.queue)
}

// QueueCapacity is how many may wait at once before further requests are refused outright.
func (a *Admission) QueueCapacity() int {
	if a == nil {
		return 0
	}
	return cap(a.queue)
}

// InFlight reports how many slots are currently taken — for the busy message and for tests.
func (a *Admission) InFlight() int {
	if a == nil {
		return 0
	}
	return len(a.slots)
}
