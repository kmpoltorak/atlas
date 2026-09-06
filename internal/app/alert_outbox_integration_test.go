package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	alertsUC "atlas/internal/application/alerts"
	analyzerUC "atlas/internal/application/analyzer"
	collectorUC "atlas/internal/application/collector"
	decisionUC "atlas/internal/application/decision"
	notificationsUC "atlas/internal/application/notifications"
	portfolioUC "atlas/internal/application/portfolio"
	watchlistUC "atlas/internal/application/watchlist"
	"atlas/internal/config"
	"atlas/internal/domain/alerts"
	"atlas/internal/domain/decision"
	infraNotifications "atlas/internal/infrastructure/notifications"
	"atlas/internal/infrastructure/persistence"
	"atlas/internal/observability"

	"go.uber.org/zap"
)

// connectOutboxDB opens the real database the outbox lives in, skipping cleanly when
// TEST_DATABASE_DSN isn't set — the same gating every other integration test here uses. These
// tests are deliberately not mocked: per-channel retry state surviving a restart is a claim
// about the database, and a fake would only prove the fake agrees with itself.
func connectOutboxDB(t *testing.T) *persistence.TimescaleDB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN not set; skipping alert outbox integration test")
	}
	db, err := persistence.NewTimescaleDB(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// outboxApplication wires a real database behind an Application with two notification channels.
func outboxApplication(t *testing.T, storage *persistence.TimescaleDB, urls ...string) *Application {
	t.Helper()
	watchlist := watchlistUC.NewUseCase()
	if err := watchlist.Add("AAPL", "2026-08-05T00:00:00Z"); err != nil {
		t.Fatalf("seed watchlist: %v", err)
	}
	// One client per channel kind, which is also the production invariant (see app.New): the
	// outbox identifies a channel by name, so two clients sharing a name would be
	// indistinguishable to it.
	constructors := []func(string) infraNotifications.Notifier{
		func(url string) infraNotifications.Notifier { return infraNotifications.NewDiscordClient(url) },
		func(url string) infraNotifications.Notifier { return infraNotifications.NewSlackClient(url) },
	}
	clients := make([]infraNotifications.Notifier, 0, len(urls))
	for i, url := range urls {
		clients = append(clients, constructors[i](url))
	}
	collector := &collectorUC.UseCase{Provider: loopStubProvider{}}
	orchestrator := decisionUC.NewOrchestrator(analyzerUC.NewUseCase(), portfolioUC.NewUseCase(), decisionUC.NewUseCase())
	return &Application{
		Config:        config.Config{},
		Metrics:       observability.NewMetrics(),
		Logger:        zap.NewNop(),
		Storage:       storage,
		Notifications: notificationsUC.NewUseCase(clients...),
		Watchlist:     watchlist,
		Alerts:        alertsUC.NewUseCase(watchlist, collector, orchestrator, 30),
	}
}

// A failing channel must not cause the alert to be redelivered to a channel that already
// succeeded — and, unlike the in-memory retry map this replaced, that per-channel state has to
// survive the process going away. The second Application below is a fresh one: it shares nothing
// with the first except the database.
func TestOutboxRetriesOnlyTheFailedChannelAcrossRestarts(t *testing.T) {
	storage := connectOutboxDB(t)

	var callsA, callsB int32
	serverA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callsA, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer serverA.Close()
	serverB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Fails for the whole lifetime of the "first process" below.
		atomic.AddInt32(&callsB, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer serverB.Close()

	before := outboxApplication(t, storage, serverA.URL, serverB.URL)
	// These tests exercise delivery bookkeeping, not leadership, so they take the lease first —
	// enqueuing is fenced on it (see persistence.requireLeadership).
	leader := newLeader()
	before.Leader = leader
	becomeLeaderAs(t, storage, leader)
	embed := buildAlertEmbed(stubAlert("AAPL", "BUY"))
	payload, err := persistence.MarshalAlertPayload(embed)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	clearAlertState(t, storage, "AAPL")
	id, err := storage.EnqueueAlert(leader.Identity(), leader.Epoch(), "AAPL", "BUY", payload,
		before.Notifications.ChannelNames())
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	t.Cleanup(func() { _ = storage.DeleteAlertState("AAPL") })

	before.deliverPendingAlerts(context.Background(), 0)

	pending, err := storage.PendingAlerts(10)
	if err != nil {
		t.Fatalf("read pending: %v", err)
	}
	if !containsOutboxID(pending, id) {
		t.Fatal("an alert whose channel failed must stay pending, not be marked delivered")
	}

	// "Restart": a brand-new Application, and this time channel B is healthy.
	serverB.Close()
	healthyB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&callsB, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer healthyB.Close()

	after := outboxApplication(t, storage, serverA.URL, healthyB.URL)
	after.Leader = newLeader()
	becomeLeaderAs(t, storage, after.Leader)
	after.deliverPendingAlerts(context.Background(), 0)

	pending, err = storage.PendingAlerts(10)
	if err != nil {
		t.Fatalf("read pending after retry: %v", err)
	}
	if containsOutboxID(pending, id) {
		t.Fatal("a fully delivered alert must not stay pending")
	}
}

// The row exists before anything is sent, which is what makes a crash mid-send recoverable. The
// old in-memory map could only ever record a send that had already been attempted.
func TestEnqueueRecordsTheAlertBeforeAnyDeliveryAttempt(t *testing.T) {
	storage := connectOutboxDB(t)

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	app := outboxApplication(t, storage, server.URL)
	app.Leader = newLeader()
	becomeLeaderAs(t, storage, app.Leader)
	// advanceCursor writes the cursor as well as the outbox row, so both have to be cleaned up —
	// a stale cursor row would make the live instance believe it had already announced this.
	clearAlertState(t, storage, "MSFT")
	if err := app.advanceCursor(context.Background(), stubAlert("MSFT", "SELL"), app.Leader.Epoch()); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if atomic.LoadInt32(&calls) != 0 {
		t.Fatal("enqueue must not send; recording and delivering are separate steps on purpose")
	}
	pending, err := storage.PendingAlerts(50)
	if err != nil {
		t.Fatalf("read pending: %v", err)
	}
	found := false
	for _, entry := range pending {
		if entry.Symbol == "MSFT" && entry.Action == "SELL" {
			found = true
			t.Cleanup(func() { _ = storage.DeleteAlertState(entry.Symbol) })
		}
	}
	if !found {
		t.Fatal("expected the alert to be durably recorded before delivery")
	}

	app.deliverPendingAlerts(context.Background(), 0)
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected exactly one delivery attempt, got %d", calls)
	}
}

// stubAlert is the minimum an Embed needs; these tests are about delivery bookkeeping, not the
// contents of the message.
func stubAlert(symbol, action string) alerts.Alert {
	return alerts.Alert{
		Recommendation: decision.Recommendation{
			Symbol: symbol, Action: action, Confidence: 0.9,
			Reason: []string{"integration test"},
		},
		TriggeredAt: "2026-09-05T10:00:00Z",
	}
}

// becomeLeaderAs makes leader hold the real lease, so fenced writes from it succeed.
//
// It force-releases first: the test database may be shared with a running instance that legitimately
// holds the lease, and these tests are about what a leader can do, not about winning an election.
func becomeLeaderAs(t *testing.T, storage *persistence.TimescaleDB, leader *Leader) string {
	t.Helper()
	if err := storage.ForceReleaseLeadership(); err != nil {
		t.Fatalf("clear the lease: %v", err)
	}
	acquired, _, epoch, err := storage.AcquireLeadership(leader.Identity(), time.Minute)
	if err != nil || !acquired {
		t.Fatalf("take the lease: acquired=%v err=%v", acquired, err)
	}
	leader.holding.Store(true)
	leader.epoch.Store(epoch)
	// The real epoch, not a placeholder: Snapshot hands the term and this epoch out together, and
	// a test that fenced writes on 0 would prove nothing about the token that actually guards them.
	leader.beginTerm(epoch)
	t.Cleanup(func() { _ = storage.ReleaseLeadership(leader.Identity()) })
	return leader.Identity()
}

func containsOutboxID(entries []persistence.OutboxEntry, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}
