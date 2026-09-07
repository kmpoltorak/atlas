package app

import (
	"testing"
	"time"
)

// The 2026-09-06 verification's third operational gap: leadership was a boolean the renewal loop
// flipped, so a process whose renewals had stalled kept reporting itself leader for as long as
// the stall lasted — long after the database had let someone else take the lease.
func TestLeadershipEndsWhenTheLeaseExpiresWithoutARenewal(t *testing.T) {
	leader := newLeader()
	leader.holding.Store(true)
	leader.beginTerm(1)

	expired := make(chan struct{})
	leader.setLease(time.Now().Add(50*time.Millisecond), func() { close(expired) })
	if !leader.IsLeader() {
		t.Fatal("a freshly acquired lease is leadership")
	}

	select {
	case <-expired:
	case <-time.After(5 * time.Second):
		t.Fatal("the lease expired and nothing stood the process down")
	}
	if leader.IsLeader() {
		t.Fatal("an expired lease is not leadership")
	}
	term, epoch := leader.Snapshot()
	select {
	case <-term.Done():
	default:
		t.Fatal("work running under an expired term must be cancelled, not left running")
	}
	if epoch != 0 {
		t.Fatalf("an ended term carries no fencing epoch, got %d", epoch)
	}
}

// The watchdog is the backstop; the clock is the answer. Even if the timer has not run yet — a
// paused container, a scheduler that did not get to it — a lease whose deadline has passed must
// not read as leadership.
func TestIsLeaderIgnoresAHeldFlagPastTheLeaseDeadline(t *testing.T) {
	leader := newLeader()
	leader.holding.Store(true)
	leader.beginTerm(1)
	leader.expiry.Store(time.Now().Add(-time.Second).UnixNano())

	if leader.IsLeader() {
		t.Fatal("a lease that ran out a second ago is not leadership, whatever the flag says")
	}
}

// A renewal is what pushes the deadline out; renewing must not trip the watchdog it re-arms.
func TestRenewingTheLeaseExtendsIt(t *testing.T) {
	leader := newLeader()
	leader.holding.Store(true)
	leader.beginTerm(1)

	stoodDown := make(chan struct{}, 1)
	leader.setLease(time.Now().Add(40*time.Millisecond), func() { stoodDown <- struct{}{} })
	leader.setLease(time.Now().Add(10*time.Second), func() { stoodDown <- struct{}{} })

	time.Sleep(150 * time.Millisecond)
	select {
	case <-stoodDown:
		t.Fatal("a renewed lease must not stand the process down when the old deadline passes")
	default:
	}
	if !leader.IsLeader() {
		t.Fatal("a renewed lease is still leadership")
	}
}

// With no database there is nothing to coordinate through, so leadership has no deadline — the
// single-process case must not stand itself down.
func TestLeadershipWithoutADatabaseNeverExpires(t *testing.T) {
	app := &Application{Leader: newLeader()}
	release := app.startLeaderElection(make(chan struct{}))
	defer release()

	if !app.Leader.IsLeader() {
		t.Fatal("with no database configured this process leads unconditionally")
	}
}
