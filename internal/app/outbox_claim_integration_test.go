package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"atlas/internal/infrastructure/persistence"
)

// The failure the 2026-09-06 verification named: a worker whose claim lapsed mid-send finishes,
// reports its result, and that stale result replaces the state of the worker that has since
// claimed and completed the same row — restoring channels already delivered to, or clearing the
// successor's claim. That is worse than the duplicate send at-least-once admits to, because it
// loses delivery state rather than repeating it.
//
// Fencing on the claim generation is what refuses that write. It runs against a real database
// because the refusal is a property of the UPDATE's WHERE clause, not of any Go code.
func TestAStaleClaimCannotOverwriteTheSuccessorsDeliveryState(t *testing.T) {
	storage := connectOutboxDB(t)
	clearAlertState(t, storage, "CLAIMFENCE")

	app := outboxApplication(t, storage, "http://127.0.0.1:1")
	app.Leader = newLeader()
	becomeLeaderAs(t, storage, app.Leader)

	payload, err := persistence.MarshalAlertPayload(buildAlertEmbed(stubAlert("CLAIMFENCE", "BUY")))
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	id, err := storage.EnqueueAlert(app.Leader.Identity(), app.Leader.Epoch(),
		"CLAIMFENCE", "BUY", payload, app.Notifications.ChannelNames())
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// The first worker claims the row. Its claim is deliberately given no time at all, so the row
	// is immediately available again — exactly the state a slow send leaves behind.
	first, err := storage.ClaimPendingAlerts("worker-one", 10, 0)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	stale := findClaim(t, first, id)

	// The successor claims the same row and finishes it.
	second, err := storage.ClaimPendingAlerts("worker-two", 10, time.Minute)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	current := findClaim(t, second, id)
	if current.ClaimSeq == stale.ClaimSeq {
		t.Fatal("a second claim must get its own generation, or an acknowledgement cannot tell the two apart")
	}
	if err := storage.RecordAlertDelivery(current.ID, current.ClaimSeq, nil, nil); err != nil {
		t.Fatalf("successor acknowledgement: %v", err)
	}

	// Now the original worker finally finishes, reporting that every channel still owes the alert.
	// Applied, this would resurrect a delivered notification and send it again.
	err = storage.RecordAlertDelivery(stale.ID, stale.ClaimSeq, stale.PendingChannels, errors.New("slow channel"))
	if !errors.Is(err, persistence.ErrClaimLost) {
		t.Fatalf("a superseded claim must be refused, got %v", err)
	}

	pending, err := storage.PendingAlerts(50)
	if err != nil {
		t.Fatalf("read pending: %v", err)
	}
	if containsOutboxID(pending, id) {
		t.Fatal("the stale acknowledgement resurrected an alert the successor had already delivered")
	}
}

// The ORDER BY in the claim's subquery chooses which rows are taken; it says nothing about the
// order RETURNING emits them in. A user reading a notification feed expects the oldest signal
// first, so the ordering has to be asserted rather than assumed.
func TestClaimedAlertsComeBackOldestFirst(t *testing.T) {
	storage := connectOutboxDB(t)
	symbols := []string{"CLAIMORD1", "CLAIMORD2", "CLAIMORD3", "CLAIMORD4"}
	clearAlertState(t, storage, symbols...)

	app := outboxApplication(t, storage, "http://127.0.0.1:1")
	app.Leader = newLeader()
	becomeLeaderAs(t, storage, app.Leader)

	for _, symbol := range symbols {
		payload, err := persistence.MarshalAlertPayload(buildAlertEmbed(stubAlert(symbol, "BUY")))
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		if _, err := storage.EnqueueAlert(app.Leader.Identity(), app.Leader.Epoch(),
			symbol, "BUY", payload, app.Notifications.ChannelNames()); err != nil {
			t.Fatalf("enqueue %s: %v", symbol, err)
		}
		// created_at has to actually differ, or "oldest first" has nothing to order by.
		time.Sleep(5 * time.Millisecond)
	}

	claimed, err := storage.ClaimPendingAlerts("ordering-worker", 100, time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	t.Cleanup(func() {
		for _, entry := range claimed {
			_ = storage.RecordAlertDelivery(entry.ID, entry.ClaimSeq, nil, nil)
		}
	})

	previous := time.Time{}
	for _, entry := range claimed {
		if !previous.IsZero() && entry.CreatedAt.Before(previous) {
			t.Fatalf("claimed alerts are not oldest-first: %v came after %v", entry.CreatedAt, previous)
		}
		previous = entry.CreatedAt
	}
}

// The delivery loop stops sending once its claims lapse, which it can only do if a claim tells it
// when that is. This asserts the two facts that guard depends on: a claim carries a real future
// deadline, and once past it the row belongs to whoever takes it next.
func TestAClaimCarriesADeadlineAndLapsesToTheNextWorker(t *testing.T) {
	storage := connectOutboxDB(t)
	clearAlertState(t, storage, "CLAIMEXP")

	app := outboxApplication(t, storage, "http://127.0.0.1:1")
	app.Leader = newLeader()
	becomeLeaderAs(t, storage, app.Leader)

	payload, err := persistence.MarshalAlertPayload(buildAlertEmbed(stubAlert("CLAIMEXP", "BUY")))
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	id, err := storage.EnqueueAlert(app.Leader.Identity(), app.Leader.Epoch(),
		"CLAIMEXP", "BUY", payload, app.Notifications.ChannelNames())
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// A short but real TTL: the row has to be genuinely held for a while and then genuinely lapse,
	// which is the sequence a slow send produces.
	const shortTTL = time.Second
	live, err := storage.ClaimPendingAlerts("worker-one", maxOutboxBatch, shortTTL)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	held := findClaim(t, live, id)
	if !held.ClaimExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("a fresh claim must carry a future deadline, got %v", held.ClaimExpiresAt)
	}

	// While the claim is live nobody else can take the row — this is what makes the deadline the
	// only thing standing between two workers and the same alert.
	contended, err := storage.ClaimPendingAlerts("worker-two", maxOutboxBatch, shortTTL)
	if err != nil {
		t.Fatalf("contended claim: %v", err)
	}
	if containsOutboxID(contended, id) {
		t.Fatal("a live claim must not be takeable by a second worker")
	}

	// Once it lapses the row is immediately somebody else's, with no cleanup step in between.
	time.Sleep(shortTTL + 200*time.Millisecond)
	successor, err := storage.ClaimPendingAlerts("worker-two", maxOutboxBatch, outboxClaimTTL)
	if err != nil {
		t.Fatalf("successor claim: %v", err)
	}
	taken := findClaim(t, successor, id)
	if taken.ClaimSeq <= held.ClaimSeq {
		t.Fatalf("taking over a lapsed claim must advance the generation: %d then %d",
			held.ClaimSeq, taken.ClaimSeq)
	}
	t.Cleanup(func() { _ = storage.RecordAlertDelivery(taken.ID, taken.ClaimSeq, nil, nil) })
}

func findClaim(t *testing.T, entries []persistence.OutboxEntry, id string) persistence.OutboxEntry {
	t.Helper()
	for _, entry := range entries {
		if entry.ID == id {
			return entry
		}
	}
	t.Fatalf("expected to claim outbox entry %s, got %d other entries", id, len(entries))
	return persistence.OutboxEntry{}
}

// Shutdown must not release the lease while a send is still in flight. Cancelling the term aborts
// the I/O, but "aborted" is not "returned": the stop function has to join the polling worker, or
// the successor starts while this process is still writing.
func TestShutdownWaitsForAnInFlightSendBeforeReleasingTheLease(t *testing.T) {
	storage := connectOutboxDB(t)
	clearAlertState(t, storage, "SHUTDOWNSEND")

	// A channel that blocks until the request context is cancelled — a send that is genuinely
	// still in flight when shutdown begins. It also releases on teardown: a client that hangs up
	// does not always cancel the server's own request context promptly, and httptest's Close
	// waits for outstanding handlers, so without this the test's cleanup deadlocks on a handler
	// it deliberately parked. What is being asserted is the client side abandoning the send.
	teardown := make(chan struct{})
	blocking := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-teardown:
		}
	}))
	defer func() {
		close(teardown)
		blocking.Close()
	}()

	app := outboxApplication(t, storage, blocking.URL)
	app.Leader = newLeader()
	becomeLeaderAs(t, storage, app.Leader)

	payload, err := persistence.MarshalAlertPayload(buildAlertEmbed(stubAlert("SHUTDOWNSEND", "BUY")))
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if _, err := storage.EnqueueAlert(app.Leader.Identity(), app.Leader.Epoch(),
		"SHUTDOWNSEND", "BUY", payload, app.Notifications.ChannelNames()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	ctx, epoch := app.Leader.Snapshot()
	delivering := make(chan struct{})
	go func() {
		defer close(delivering)
		app.deliverPendingAlerts(ctx, epoch)
	}()

	// Let the send reach the blocking server, then end the term the way shutdown does.
	time.Sleep(300 * time.Millisecond)
	app.Leader.endTerm()

	select {
	case <-delivering:
		// The send was abandoned because the term's context reached the HTTP client. Without ctx
		// on Notifier.SendEmbed this sits here until the server is closed.
	case <-time.After(5 * time.Second):
		t.Fatal("a send outlived the leadership term; cancellation is not reaching the notification client")
	}
}

// A term is per-lease, not per-process. Losing the lease and taking it again is a *different*
// term with a different fencing epoch, and work started under the old one must not write under
// the new one's authority.
func TestReacquiringTheLeaseStartsANewTermAndEpoch(t *testing.T) {
	storage := connectOutboxDB(t)

	leader := newLeader()
	becomeLeaderAs(t, storage, leader)
	firstTerm, firstEpoch := leader.Snapshot()

	// Lose it: another process takes over, then hands it back.
	leader.endTerm()
	leader.holding.Store(false)
	if err := storage.ForceReleaseLeadership(); err != nil {
		t.Fatalf("clear the lease: %v", err)
	}
	other := newLeader()
	becomeLeaderAs(t, storage, other)
	becomeLeaderAs(t, storage, leader)
	secondTerm, secondEpoch := leader.Snapshot()

	if firstTerm.Err() == nil {
		t.Fatal("the term of a lost lease must be cancelled, or work started under it keeps running")
	}
	if secondTerm.Err() != nil {
		t.Fatal("a reacquired lease must have a live term")
	}
	if secondEpoch == firstEpoch {
		t.Fatalf("a reacquired lease must carry a new fencing epoch, got %d both times", firstEpoch)
	}
}
