package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// leaderTTL is how long a lease lasts, and leaderRenewInterval how often the holder renews it.
// Renewing at a third of the TTL means two consecutive renewal failures (a slow query, a brief
// network blip) still leave a full third of the lease in hand before anyone else can take over.
//
// The gap between them is also the worst-case handover delay after an unclean death: up to
// leaderTTL of nobody polling. Two minutes is chosen against the 300s default poll interval —
// less than one cycle, so a failover costs at most one skipped poll, not a visible outage.
const (
	leaderTTL            = 2 * time.Minute
	leaderRenewInterval  = 40 * time.Second
	leaderRetryInterval  = 30 * time.Second
	leaderIdentityLength = 8
)

// Leader tracks whether this process currently holds the polling lease. Exactly one replica does
// the work that must not be duplicated: evaluating the watchlist, writing recommendations, and
// sending notifications. Everything else — serving HTTP, reading state — every replica does.
//
// This is what makes more than one replica safe. Without it, each process polled independently
// and each sent its own copy of every notification (see docs/deployment.md's "Scaling").
type Leader struct {
	identity string
	holding  atomic.Bool
	// epoch is the fencing token from the current lease (see persistence.requireLeadership). It
	// is what makes a write refuse to land after this process has been superseded.
	epoch atomic.Int64

	// mu guards term/cancelTerm, which are replaced on every acquisition.
	mu sync.Mutex
	// term is cancelled the moment leadership is lost or shutdown begins. Every batch of
	// singleton work runs under it, so losing the lease stops work already in flight rather than
	// only preventing the next batch from starting — the gap the 2026-09-06 review found.
	term       context.Context
	cancelTerm context.CancelFunc
	// termEpoch is the fencing token this term was acquired under, kept beside the term so the
	// two can be read as a pair. See Snapshot.
	termEpoch int64
}

// newLeader builds a per-process identity. It is deliberately not just the hostname: two pods can
// share one, and a restarted process must re-acquire the lease rather than silently inherit the
// one its previous incarnation was holding when it died.
func newLeader() *Leader {
	suffix := make([]byte, leaderIdentityLength)
	if _, err := rand.Read(suffix); err != nil {
		// A weaker identity is still better than refusing to start; the worst case is that two
		// processes could collide on it, which the lease's own expiry still bounds.
		return &Leader{identity: hostname() + "-" + time.Now().UTC().Format("150405.000")}
	}
	return &Leader{identity: hostname() + "-" + hex.EncodeToString(suffix)}
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		return "atlas"
	}
	return name
}

// IsLeader reports whether this process should be doing the singleton work right now.
//
// This is a hint for deciding whether to *start* a batch, never a guarantee that it may finish:
// the lease can be lost at any point after it returns true. Work already running is stopped by
// Term()'s cancellation, and writes are fenced by the epoch — see persistence.requireLeadership.
func (l *Leader) IsLeader() bool { return l != nil && l.holding.Load() }

// Snapshot returns the leadership term together with the fencing epoch that term was acquired
// under. The term is a context cancelled when this process stops being leader, so a batch stops
// partway instead of running to completion on behalf of a lease somebody else now holds. Never
// nil: a non-leader gets an already-cancelled context and epoch 0.
//
// Every batch takes the pair once and uses it throughout.
//
// Reading Epoch() separately at each write was the gap the 2026-09-06 verification named: a batch
// could begin under one lease, lose it, reacquire it, and then write with the *new* epoch — which
// is exactly the late write the fencing token exists to reject. Held together, an epoch that no
// longer matches the lease makes the write fail, and the cancelled term stops the batch anyway.
func (l *Leader) Snapshot() (context.Context, int64) {
	if l == nil {
		return canceledContext(), 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.term == nil {
		return canceledContext(), 0
	}
	return l.term, l.termEpoch
}

// Epoch is the fencing token of the currently held lease, passed to every fenced write.
func (l *Leader) Epoch() int64 {
	if l == nil {
		return 0
	}
	return l.epoch.Load()
}

// beginTerm starts a fresh cancellable term. Called on acquisition, not on renewal — a renewal
// continues the same term, and cancelling it would abort the leader's own in-flight work.
func (l *Leader) beginTerm(epoch int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancelTerm != nil {
		l.cancelTerm()
	}
	l.term, l.cancelTerm = context.WithCancel(context.Background())
	l.termEpoch = epoch
}

// endTerm cancels the current term, stopping any batch running under it.
func (l *Leader) endTerm() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancelTerm != nil {
		l.cancelTerm()
		l.cancelTerm = nil
	}
	l.term = nil
	l.termEpoch = 0
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// Identity is this process's lease holder id — shown on the Account page so "which replica is
// polling" is answerable from the UI.
func (l *Leader) Identity() string {
	if l == nil {
		return ""
	}
	return l.identity
}

// startLeaderElection runs the acquire/renew loop until stop is closed, returning a function that
// releases the lease on the way out so a clean shutdown hands over in seconds rather than after a
// full TTL of nobody polling.
//
// With no database configured (tests, a degraded start) this process simply declares itself the
// leader: there is no shared state to coordinate through, so there is nothing to coordinate.
func (a *Application) startLeaderElection(stop <-chan struct{}) func() {
	if a.Storage == nil {
		a.Leader.holding.Store(true)
		a.Leader.beginTerm(0)
		return func() { a.Leader.endTerm() }
	}

	tryAcquire := func() time.Duration {
		acquired, until, epoch, err := a.Storage.AcquireLeadership(a.Leader.identity, leaderTTL)
		if err != nil {
			// A database error is not evidence that someone else is leading, but continuing to
			// act as leader without a renewed lease is exactly the split brain the lease exists
			// to prevent — so step down, cancelling whatever is running, and retry.
			if a.Leader.holding.Swap(false) {
				a.Leader.endTerm()
			}
			a.Logger.Warn("leader election failed; standing down until the next attempt", zap.Error(err))
			return leaderRetryInterval
		}
		was := a.Leader.holding.Swap(acquired)
		if acquired {
			// A new epoch means the lease changed hands — including back to this process after a
			// gap, which is a different term and must not reuse the old one's context. A renewal
			// keeps the same epoch and therefore the same term, so in-flight work is not aborted.
			previousEpoch := a.Leader.epoch.Swap(epoch)
			if !was || previousEpoch != epoch {
				a.Leader.beginTerm(epoch)
				a.Logger.Info("acquired the polling lease",
					zap.String("holder", a.Leader.identity), zap.Int64("epoch", epoch),
					zap.Time("until", until))
			}
			return leaderRenewInterval
		}
		if was {
			a.Leader.endTerm()
			a.Logger.Info("lost the polling lease", zap.String("holder", a.Leader.identity))
		}
		return leaderRetryInterval
	}

	// renewals is joined before the lease is released, so a renewal already in flight cannot
	// land after another process has taken over — the shutdown race the review named.
	var renewals sync.WaitGroup
	renewals.Add(1)

	next := tryAcquire()
	timer := time.NewTimer(next)
	go func() {
		defer renewals.Done()
		defer timer.Stop()
		for {
			select {
			case <-timer.C:
				timer.Reset(tryAcquire())
			case <-stop:
				return
			}
		}
	}()

	return func() {
		// Order matters, and this is the order: stop taking new work, let what is running finish
		// or abort, wait for the renewal loop to exit, and only then hand the lease over. Any
		// other order lets the successor start while this process is still writing.
		a.Leader.endTerm()
		renewals.Wait()
		if a.Leader.holding.Swap(false) {
			if err := a.Storage.ReleaseLeadership(a.Leader.identity); err != nil {
				a.Logger.Warn("failed to release the polling lease", zap.Error(err))
			}
		}
	}
}
