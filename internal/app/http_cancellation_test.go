package app

import (
	"context"

	watchlistUC "atlas/internal/application/watchlist"

	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The 2026-09-06 review's specific objection: testing Storage.WithContext directly proves the
// handle honours a context, not that an HTTP request's cancellation ever reaches the repository a
// use case actually queries. It did not — the request-owned portfolio view carried the shared,
// unscoped repository, so scoping Application.Storage changed nothing for portfolio writes.
//
// This drives a real handler with a real client-side cancellation and asserts the query is
// abandoned rather than run to completion for a response nobody will read.
func TestCancellingAnHTTPRequestAbandonsThePortfolioQuery(t *testing.T) {
	app, handler := newHTTPTestApplication(t)

	// Block the database for longer than this request will live, so the handler is genuinely
	// waiting on the database when the client hangs up rather than finishing first.
	release := blockPortfolioTable(t, app)
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/portfolio", nil).WithContext(ctx)
	request.Header.Set("Sec-Fetch-Site", "same-origin")

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}()

	// Give the handler time to reach the blocked query, then hang up.
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// The handler returned because the query was cancelled. Without the scoped repository it
		// would sit on the lock until the 10s query deadline instead.
	case <-time.After(5 * time.Second):
		t.Fatal("the handler did not return after the client disconnected; cancellation is not reaching the repository")
	}
}

// A write goes through the same request-owned view, so it has to be cancellable too — this is the
// path the review pointed at specifically (ForPortfolio's repository, not Application.Storage).
func TestCancellingAnHTTPRequestAbandonsAPortfolioWrite(t *testing.T) {
	app, handler := newHTTPTestApplication(t)

	release := blockPortfolioTable(t, app)
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	body := `{"symbol":"TESTCANCEL","type":"BUY","quantity":1,"price":10,"price_in_pln":10,"timestamp":"2026-09-05T09:00:00Z"}`
	request := httptest.NewRequest(http.MethodPost, "/portfolio", strings.NewReader(body)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled write kept running; the request-owned view is not using the scoped repository")
	}

	// Release before reading: the verification query would otherwise queue behind the same lock.
	release()

	positions, err := app.Portfolio.GetAllPositions()
	if err != nil {
		t.Fatalf("read positions: %v", err)
	}
	if quantityOf(positions, "TESTCANCEL") != 0 {
		t.Fatal("a cancelled write was committed anyway")
	}
}

// blockPortfolioTable holds an exclusive lock on positions until the returned function is called,
// so any query touching it waits.
func blockPortfolioTable(t *testing.T, app *Application) func() {
	t.Helper()
	return blockTable(t, app, "positions")
}

// The 2026-09-06 verification's finding 5: scoping Application.Storage was never enough on its
// own, and the watchlist paths were the ones still holding the repository they were constructed
// with at startup. A disconnected import or mutation went on executing against nobody until the
// repository's own ten-second deadline.
func TestCancellingAnHTTPRequestAbandonsTheWatchlistQuery(t *testing.T) {
	app, handler := newWatchlistHTTPTestApplication(t)

	release := blockTable(t, app, "watchlist_items")
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/watchlist", nil).WithContext(ctx)
	request.Header.Set("Sec-Fetch-Site", "same-origin")

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler did not return after the client disconnected; the watchlist use case is not request-scoped")
	}
}

// The write path matters more than the read: an abandoned import is the one that holds a lock.
func TestCancellingAnHTTPRequestAbandonsAWatchlistImport(t *testing.T) {
	app, handler := newWatchlistHTTPTestApplication(t)

	release := blockTable(t, app, "watchlist_items")
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	body := `{"items":[{"symbol":"TESTCANCELWL","added_at":"2026-01-02T03:04:05Z"}]}`
	request := httptest.NewRequest(http.MethodPost, "/watchlist/import", strings.NewReader(body)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "same-origin")

	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled import kept running; the watchlist repository is not request-scoped")
	}

	// Release before reading: the verification query would otherwise queue behind the same lock.
	release()

	items, err := app.Watchlist.List()
	if err != nil {
		t.Fatalf("read watchlist: %v", err)
	}
	for _, item := range items {
		if item.Symbol == "TESTCANCELWL" {
			t.Fatal("a cancelled import was committed anyway")
		}
	}
}

// newWatchlistHTTPTestApplication is newHTTPTestApplication plus a database-backed watchlist use
// case — the thing being scoped here.
func newWatchlistHTTPTestApplication(t *testing.T) (*Application, http.Handler) {
	t.Helper()
	app, _ := newHTTPTestApplication(t)
	uc, err := watchlistUC.NewUseCaseWithRepository(app.Storage)
	if err != nil {
		t.Fatalf("watchlist use case: %v", err)
	}
	app.Watchlist = uc
	return app, app.NewHandler()
}

// blockTable holds an exclusive lock on one table until the returned function is called, so any
// query touching it waits. Uses its own connection: the lock lives on the transaction.
func blockTable(t *testing.T, app *Application, table string) func() {
	t.Helper()
	blocker, err := app.Storage.BeginBlockingLock(context.Background(), table)
	if err != nil {
		t.Fatalf("take a blocking lock on %s: %v", table, err)
	}
	released := false
	return func() {
		if released {
			return
		}
		released = true
		blocker()
	}
}
