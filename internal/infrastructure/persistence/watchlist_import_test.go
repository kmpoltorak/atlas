package persistence

import (
	"errors"
	"testing"
	"time"

	"atlas/internal/domain/watchlist"
)

// clearWatchlist empties the shared test watchlist so each case starts from a known state.
func clearWatchlist(t *testing.T, db *TimescaleDB) []watchlist.Item {
	t.Helper()
	before, err := db.GetItems()
	if err != nil {
		t.Fatalf("read watchlist: %v", err)
	}
	if _, err := db.ReplaceWatchlist(func([]watchlist.Item) ([]watchlist.Item, error) {
		return nil, nil
	}); err != nil {
		t.Fatalf("clear watchlist: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.ReplaceWatchlist(func([]watchlist.Item) ([]watchlist.Item, error) {
			return before, nil
		}); err != nil {
			t.Errorf("restore watchlist: %v", err)
		}
	})
	return before
}

// The exported timestamp has to survive the round trip. It did not: SaveItem ignored AddedAt and
// let the column default apply, so a database-backed import silently reset every symbol's history
// to "now" — invisible to the in-memory tests, which never went through SQL.
func TestWatchlistAddedAtRoundTripsThroughTheDatabase(t *testing.T) {
	db := connectTestDB(t)
	clearWatchlist(t, db)

	if err := db.SaveItem(watchlist.Item{Symbol: "TESTTS", AddedAt: "2024-03-07T09:15:00Z"}); err != nil {
		t.Fatalf("save: %v", err)
	}

	items, err := db.GetItems()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, item := range items {
		if item.Symbol != "TESTTS" {
			continue
		}
		if item.AddedAt != "2024-03-07T09:15:00Z" {
			t.Fatalf("added_at did not round-trip, got %q", item.AddedAt)
		}
		return
	}
	t.Fatal("the saved item was not read back")
}

func TestWatchlistRejectsAnUnparseableAddedAt(t *testing.T) {
	db := connectTestDB(t)

	if err := db.SaveItem(watchlist.Item{Symbol: "TESTBADTS", AddedAt: "not-a-timestamp"}); err == nil {
		t.Fatal("an unparseable added_at must fail rather than silently becoming now()")
	}
}

// A failure partway through an import must leave the watchlist exactly as it was. Item-by-item
// commits left a partial replacement instead — half the file applied, no way to tell which half.
func TestFailedImportRollsBackEntirely(t *testing.T) {
	db := connectTestDB(t)
	clearWatchlist(t, db)

	seed := []watchlist.Item{
		{Symbol: "TESTKEEPA", AddedAt: "2025-01-01T00:00:00Z"},
		{Symbol: "TESTKEEPB", AddedAt: "2025-01-02T00:00:00Z"},
	}
	if _, err := db.ReplaceWatchlist(func([]watchlist.Item) ([]watchlist.Item, error) {
		return seed, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// An import whose later item is invalid: the first is fine, the second cannot be written.
	_, err := db.ReplaceWatchlist(func([]watchlist.Item) ([]watchlist.Item, error) {
		return []watchlist.Item{
			{Symbol: "TESTNEW", AddedAt: "2026-01-01T00:00:00Z"},
			{Symbol: "TESTBROKEN", AddedAt: "nonsense"},
		}, nil
	})
	if err == nil {
		t.Fatal("an import with an invalid item must fail")
	}

	items, err := db.GetItems()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	symbols := map[string]bool{}
	for _, item := range items {
		symbols[item.Symbol] = true
	}
	if !symbols["TESTKEEPA"] || !symbols["TESTKEEPB"] {
		t.Fatalf("a failed import removed existing items: %+v", items)
	}
	if symbols["TESTNEW"] {
		t.Fatalf("a failed import committed part of its changes: %+v", items)
	}
}

// A plan that returns an error aborts before any write — the caller's own validation failing
// must not leave the database half-changed either.
func TestAFailingPlanWritesNothing(t *testing.T) {
	db := connectTestDB(t)
	clearWatchlist(t, db)

	if _, err := db.ReplaceWatchlist(func([]watchlist.Item) ([]watchlist.Item, error) {
		return []watchlist.Item{{Symbol: "TESTPLAN", AddedAt: "2026-01-01T00:00:00Z"}}, nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	planErr := errors.New("caller rejected the import")
	if _, err := db.ReplaceWatchlist(func([]watchlist.Item) ([]watchlist.Item, error) {
		return nil, planErr
	}); !errors.Is(err, planErr) {
		t.Fatalf("expected the plan's own error, got %v", err)
	}

	items, err := db.GetItems()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, item := range items {
		if item.Symbol == "TESTPLAN" {
			return // still there, as it must be
		}
	}
	t.Fatal("a failing plan deleted items anyway")
}

// Importing into a fresh installation is a perfectly ordinary first thing to do, and it failed:
// ReplaceWatchlist locked the default watchlist row with a plain SELECT, and nothing creates that
// row except SaveItem. So an import before the first individual add — or after a flush — returned
// "lock watchlist: sql: no rows in result set".
//
// The row is deleted here rather than trusting the test order: the existing import tests can pass
// purely because an earlier test happened to create it, which is precisely how this went unnoticed.
func TestImportIntoAWatchlistThatDoesNotExistYet(t *testing.T) {
	db := connectTestDB(t)
	clearWatchlist(t, db) // restores whatever was there on cleanup

	ctx, cancel := dbContext()
	defer cancel()
	if _, err := db.db.ExecContext(ctx, `DELETE FROM watchlists WHERE name = $1`, defaultWatchlistName); err != nil {
		t.Fatalf("simulate a fresh installation: %v", err)
	}

	imported, err := db.ReplaceWatchlist(func(current []watchlist.Item) ([]watchlist.Item, error) {
		if len(current) != 0 {
			t.Errorf("a fresh watchlist must plan against nothing, got %d items", len(current))
		}
		return []watchlist.Item{{Symbol: "TESTFRESH", AddedAt: "2026-01-02T03:04:05Z"}}, nil
	})
	if err != nil {
		t.Fatalf("import into a fresh installation: %v", err)
	}
	if len(imported) != 1 {
		t.Fatalf("expected one imported item, got %d", len(imported))
	}

	items, err := db.GetItems()
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(items) != 1 || items[0].Symbol != "TESTFRESH" {
		t.Fatalf("the import did not land: %+v", items)
	}
}

// An import plans against the contents it read inside its transaction. A delete that skipped the
// parent lock could land between that read and the writes, so the import would faithfully restore
// a symbol the user had just removed — the read was consistent, the world was not.
func TestADeleteWaitsForAnImportInProgress(t *testing.T) {
	db := connectTestDB(t)
	clearWatchlist(t, db)

	if err := db.SaveItem(watchlist.Item{Symbol: "TESTRACE", AddedAt: "2026-01-02T03:04:05Z"}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	planning := make(chan struct{})
	finishPlanning := make(chan struct{})
	deleteReturned := make(chan error, 1)

	go func() {
		<-planning
		// The import is inside its transaction holding the lock. Without the lock in DeleteItem
		// this returns immediately, ahead of the import's writes.
		deleteReturned <- db.DeleteItem("TESTRACE")
	}()

	if _, err := db.ReplaceWatchlist(func(current []watchlist.Item) ([]watchlist.Item, error) {
		close(planning)
		select {
		case <-deleteReturned:
			t.Error("a concurrent delete completed while the import held the watchlist lock")
		case <-time.After(500 * time.Millisecond):
			// Blocked, as it must be.
		}
		close(finishPlanning)
		return current, nil
	}); err != nil {
		t.Fatalf("import: %v", err)
	}
	<-finishPlanning

	select {
	case err := <-deleteReturned:
		if err != nil {
			t.Fatalf("the delete must proceed once the import commits: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the delete never completed after the import committed")
	}

	items, err := db.GetItems()
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	for _, item := range items {
		if item.Symbol == "TESTRACE" {
			t.Fatal("the delete was serialized after the import, so its removal must be the final state")
		}
	}
}
