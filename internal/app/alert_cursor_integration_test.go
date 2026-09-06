package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"atlas/internal/infrastructure/persistence"
)

// clearAlertState wipes the cursor and outbox rows these tests create, so each starts from a
// known state regardless of order or of what the live instance left behind.
func clearAlertState(t *testing.T, storage *persistence.TimescaleDB, symbols ...string) {
	t.Helper()
	wipe := func() {
		for _, symbol := range symbols {
			_ = storage.DeleteAlertState(symbol)
		}
	}
	wipe()
	t.Cleanup(wipe)
}

// The cursor must be a record of what was *announced*, not of what the engine said. The
// recommendations table is written by GET /recommendations, the recommendations page and
// post-transaction refreshes, so deriving the cursor from it meant ordinary browsing could
// suppress a notification that was never sent.
func TestCursorIsNotAdvancedByRecommendationHistory(t *testing.T) {
	storage := connectOutboxDB(t)
	clearAlertState(t, storage, "AAPL")

	app := outboxApplication(t, storage, "http://127.0.0.1:1") // a channel that always fails
	app.Leader = newLeader()
	becomeLeaderAs(t, storage, app.Leader)

	// Simulate what a browser hitting /recommendations does: write history, nothing else.
	app.persistRecommendation(stubAlert("AAPL", "BUY").Recommendation)

	cursor, err := storage.AlertCursor()
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if cursor["AAPL"] != "" {
		t.Fatalf("recommendation history must not advance the announcement cursor, got %q", cursor["AAPL"])
	}
}

// The cursor and the outbox entry are one transaction: either the alert is queued and the cursor
// says so, or neither happened. A crash between them used to leave history claiming BUY with
// nothing pending, and the restart suppressed it as unchanged.
func TestCursorAndOutboxEntryCommitTogether(t *testing.T) {
	storage := connectOutboxDB(t)
	clearAlertState(t, storage, "MSFT")

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	app := outboxApplication(t, storage, server.URL)
	app.Leader = newLeader()
	becomeLeaderAs(t, storage, app.Leader)

	if err := app.advanceCursor(context.Background(), stubAlert("MSFT", "SELL"), app.Leader.Epoch()); err != nil {
		t.Fatalf("advance: %v", err)
	}

	cursor, err := storage.AlertCursor()
	if err != nil {
		t.Fatalf("read cursor: %v", err)
	}
	if cursor["MSFT"] != "SELL" {
		t.Fatalf("expected the cursor to record SELL, got %q", cursor["MSFT"])
	}
	pending, err := storage.PendingAlerts(50)
	if err != nil {
		t.Fatalf("read pending: %v", err)
	}
	queued := false
	for _, entry := range pending {
		if entry.Symbol == "MSFT" && entry.Action == "SELL" {
			queued = true
		}
	}
	if !queued {
		t.Fatal("the cursor advanced without a matching outbox entry")
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatal("advancing the cursor must not send; recording and delivering are separate steps")
	}
}

// A process that has lost the lease must not advance the cursor for a successor that is already
// polling. Expiry checks in Go race with the write they guard; the fence does not.
func TestAFormerLeaderCannotAdvanceTheCursor(t *testing.T) {
	storage := connectOutboxDB(t)
	clearAlertState(t, storage, "TSLA")

	app := outboxApplication(t, storage, "http://127.0.0.1:1")
	app.Leader = newLeader()
	becomeLeaderAs(t, storage, app.Leader)

	// Somebody else takes over. The old process still believes it is the leader.
	if err := storage.ReleaseLeadership(app.Leader.Identity()); err != nil {
		t.Fatalf("release: %v", err)
	}
	successor := newLeader()
	if acquired, _, _, err := storage.AcquireLeadership(successor.Identity(), time.Minute); err != nil || !acquired {
		t.Fatalf("successor must take the lease: acquired=%v err=%v", acquired, err)
	}
	t.Cleanup(func() { _ = storage.ReleaseLeadership(successor.Identity()) })

	err := app.advanceCursor(context.Background(), stubAlert("TSLA", "BUY"), app.Leader.Epoch())
	if err == nil {
		t.Fatal("a superseded leader must not be able to advance the cursor")
	}

	cursor, readErr := storage.AlertCursor()
	if readErr != nil {
		t.Fatalf("read cursor: %v", readErr)
	}
	if cursor["TSLA"] != "" {
		t.Fatalf("the fenced write must not have landed, got %q", cursor["TSLA"])
	}
}

// A promoted follower inherits the real cursor rather than a baseline captured at its own
// startup — which is what reading per poll, instead of seeding once, buys.
func TestAPromotedFollowerReadsTheCurrentCursor(t *testing.T) {
	storage := connectOutboxDB(t)
	clearAlertState(t, storage, "NVDA")

	leaderApp := outboxApplication(t, storage, "http://127.0.0.1:1")
	leaderApp.Leader = newLeader()
	becomeLeaderAs(t, storage, leaderApp.Leader)

	// A follower that has been running since before this transition.
	follower := outboxApplication(t, storage, "http://127.0.0.1:1")
	follower.Leader = newLeader()

	if err := leaderApp.Storage.RecordSilentTransition(
		leaderApp.Leader.Identity(), leaderApp.Leader.Epoch(), "NVDA", "BUY"); err != nil {
		t.Fatalf("leader records a transition: %v", err)
	}

	// Promotion.
	if err := storage.ReleaseLeadership(leaderApp.Leader.Identity()); err != nil {
		t.Fatalf("release: %v", err)
	}
	becomeLeaderAs(t, storage, follower.Leader)

	cursor, err := follower.alertCursor()
	if err != nil {
		t.Fatalf("promoted follower reads the cursor: %v", err)
	}
	if cursor["NVDA"] != "BUY" {
		t.Fatalf("a promoted follower must inherit the durable cursor, got %q", cursor["NVDA"])
	}
}

// Two workers — a former leader still finishing a batch and its successor — must not deliver the
// same outbox entry. The claim, not a Go-side check, is what makes that true.
func TestTwoWorkersCannotClaimTheSameAlert(t *testing.T) {
	storage := connectOutboxDB(t)
	clearAlertState(t, storage, "CLAIMTEST")

	leader := newLeader()
	becomeLeaderAs(t, storage, leader)
	payload, err := persistence.MarshalAlertPayload(buildAlertEmbed(stubAlert("CLAIMTEST", "BUY")))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := storage.EnqueueAlert(leader.Identity(), leader.Epoch(),
		"CLAIMTEST", "BUY", payload, []string{"discord"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	first, err := storage.ClaimPendingAlerts("worker-a", 50, time.Minute)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	second, err := storage.ClaimPendingAlerts("worker-b", 50, time.Minute)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}

	if !containsSymbol(first, "CLAIMTEST") {
		t.Fatal("the first worker should have claimed the entry")
	}
	if containsSymbol(second, "CLAIMTEST") {
		t.Fatal("two workers claimed the same alert; the claim is not exclusive")
	}
}

// A worker that dies mid-send must not strand the alert forever: the claim is a lease, so it
// becomes claimable again once it lapses. This is also the honest boundary of the at-least-once
// guarantee — the window in which a second attempt can happen.
func TestAnExpiredClaimBecomesAvailableAgain(t *testing.T) {
	storage := connectOutboxDB(t)
	clearAlertState(t, storage, "STALECLAIM")

	leader := newLeader()
	becomeLeaderAs(t, storage, leader)
	payload, err := persistence.MarshalAlertPayload(buildAlertEmbed(stubAlert("STALECLAIM", "SELL")))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := storage.EnqueueAlert(leader.Identity(), leader.Epoch(),
		"STALECLAIM", "SELL", payload, []string{"discord"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// A claim that has already lapsed stands in for a worker that died holding it.
	if _, err := storage.ClaimPendingAlerts("dead-worker", 50, -time.Second); err != nil {
		t.Fatalf("stale claim: %v", err)
	}
	recovered, err := storage.ClaimPendingAlerts("worker-b", 50, time.Minute)
	if err != nil {
		t.Fatalf("recovering claim: %v", err)
	}
	if !containsSymbol(recovered, "STALECLAIM") {
		t.Fatal("an expired claim must return the entry to the queue")
	}
}

// Delivery stops when the term ends, leaving the remaining claims to lapse rather than pushing
// notifications on behalf of a lease somebody else holds.
func TestDeliveryStopsWhenTheLeadershipTermEnds(t *testing.T) {
	storage := connectOutboxDB(t)
	clearAlertState(t, storage, "TERMSTOP")

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	app := outboxApplication(t, storage, server.URL)
	app.Leader = newLeader()
	becomeLeaderAs(t, storage, app.Leader)
	if err := app.advanceCursor(context.Background(), stubAlert("TERMSTOP", "BUY"), app.Leader.Epoch()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	app.deliverPendingAlerts(ctx, 0)

	if atomic.LoadInt32(&calls) != 0 {
		t.Fatalf("delivery must stop with the term, got %d sends", calls)
	}
}

func containsSymbol(entries []persistence.OutboxEntry, symbol string) bool {
	for _, entry := range entries {
		if entry.Symbol == symbol {
			return true
		}
	}
	return false
}
