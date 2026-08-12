package persistence

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"atlas/internal/domain/market"
)

// connectTestDB opens a real connection using TEST_DATABASE_DSN, skipping the test cleanly
// when it isn't set (local dev without Docker, or an environment that hasn't wired up a
// Postgres/TimescaleDB service container yet — see .github/workflows/ci.yml). Every persistence
// test uses a "TEST..."-prefixed symbol/role (TESTAAPL, TESTWATCHDUP, "correction-test", ...) —
// see cleanupTestFixtures — so this also registers the matching cleanup, run before the
// connection itself closes (t.Cleanup runs LIFO), leaving TEST_DATABASE_DSN as this test found
// it instead of permanently accumulating rows across every run.
func connectTestDB(t *testing.T) *TimescaleDB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_DSN")
	if dsn == "" {
		t.Skip("TEST_DATABASE_DSN not set; skipping TimescaleDB integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := NewTimescaleDB(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to connect to test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	t.Cleanup(func() { cleanupTestFixtures(t, db) })
	return db
}

// cleanupTestFixtures deletes every row a persistence test could have written, identified by the
// project-wide "TEST..." naming convention (see e.g. timescaledb_test.go's own TESTAAPL/
// TESTIDEMPOTENT) rather than tracking exactly which rows each individual test added — simpler,
// and correct as long as the convention holds (enforced only by test-author discipline, same as
// every other project-wide naming convention). symbol_id-scoped tables are deleted before
// symbols itself to satisfy their foreign keys; ml_model_selection is separate (role-keyed, not
// symbol-scoped) and uses the "...-test" role suffix that ml_model_selection_test.go writes.
func cleanupTestFixtures(t *testing.T, db *TimescaleDB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const bySymbol = ` WHERE symbol_id IN (SELECT id FROM symbols WHERE ticker LIKE 'TEST%')`
	for _, table := range []string{
		"prices", "indicators", "predictions", "positions", "transactions",
		"recommendations", "backtest_runs", "watchlist_items",
	} {
		if _, err := db.db.ExecContext(ctx, "DELETE FROM "+table+bySymbol); err != nil {
			t.Logf("cleanupTestFixtures: delete from %s failed: %v", table, err)
		}
	}
	if _, err := db.db.ExecContext(ctx, "DELETE FROM symbols WHERE ticker LIKE 'TEST%'"); err != nil {
		t.Logf("cleanupTestFixtures: delete from symbols failed: %v", err)
	}
	if _, err := db.db.ExecContext(ctx, "DELETE FROM ml_model_selection WHERE role LIKE '%-test'"); err != nil {
		t.Logf("cleanupTestFixtures: delete from ml_model_selection failed: %v", err)
	}
	if _, err := db.db.ExecContext(ctx, "DELETE FROM portfolios WHERE name LIKE 'TEST%'"); err != nil {
		t.Logf("cleanupTestFixtures: delete from portfolios failed: %v", err)
	}
}

// testPortfolioID creates a fresh, uniquely-named portfolio for one test — position/transaction
// tests need a real portfolio_id to scope against, and a dedicated one per test avoids leaking
// state into (or reading state left behind by) any other test. Cleaned up by
// cleanupTestFixtures's "TEST%" portfolio-name sweep.
func testPortfolioID(t *testing.T, db *TimescaleDB) string {
	t.Helper()
	p, err := db.CreatePortfolio("TEST-" + t.Name())
	if err != nil {
		t.Fatalf("failed to create test portfolio: %v", err)
	}
	return p.ID
}

func TestDSNIncludesConnectionParameters(t *testing.T) {
	dsn := DSN("localhost", "5432", "atlas", "atlas", "secret")
	for _, want := range []string{"host=localhost", "port=5432", "user=atlas", "dbname=atlas"} {
		if !strings.Contains(dsn, want) {
			t.Fatalf("expected DSN to contain %q, got %q", want, dsn)
		}
	}
}

func TestSaveBarsAndGetBarsRoundTrip(t *testing.T) {
	db := connectTestDB(t)
	symbol := "TESTAAPL"
	now := time.Now().UTC().Truncate(time.Second)
	bars := []market.Bar{
		{Symbol: symbol, Open: 100, High: 105, Low: 99, Close: 104, Volume: 1000, Timestamp: now.Add(-2 * time.Minute)},
		{Symbol: symbol, Open: 104, High: 108, Low: 103, Close: 107, Volume: 1100, Timestamp: now.Add(-1 * time.Minute)},
	}

	if err := db.SaveBars(symbol, bars); err != nil {
		t.Fatalf("SaveBars failed: %v", err)
	}

	stored, err := db.GetBars(symbol)
	if err != nil {
		t.Fatalf("GetBars failed: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("expected 2 stored bars, got %d", len(stored))
	}
	if stored[0].Close != 104 || stored[1].Close != 107 {
		t.Fatalf("unexpected bar values: %+v", stored)
	}
	if !stored[0].Timestamp.Before(stored[1].Timestamp) {
		t.Fatalf("expected bars ordered oldest first, got %+v", stored)
	}
}

func TestSaveBarsIsIdempotent(t *testing.T) {
	db := connectTestDB(t)
	symbol := "TESTIDEMPOTENT"
	ts := time.Now().UTC().Truncate(time.Second)
	original := []market.Bar{{Symbol: symbol, Open: 10, High: 11, Low: 9, Close: 10.5, Volume: 500, Timestamp: ts}}

	if err := db.SaveBars(symbol, original); err != nil {
		t.Fatalf("first SaveBars failed: %v", err)
	}
	// Same timestamp, different price — simulates re-collecting an overlapping window with a
	// corrected value. Must overwrite, not duplicate.
	updated := []market.Bar{{Symbol: symbol, Open: 10, High: 12, Low: 9, Close: 11.5, Volume: 600, Timestamp: ts}}
	if err := db.SaveBars(symbol, updated); err != nil {
		t.Fatalf("second SaveBars failed: %v", err)
	}

	stored, err := db.GetBars(symbol)
	if err != nil {
		t.Fatalf("GetBars failed: %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("expected exactly one row after re-saving the same timestamp, got %d: %+v", len(stored), stored)
	}
	if stored[0].Close != 11.5 {
		t.Fatalf("expected the row to be overwritten with the updated close, got %v", stored[0].Close)
	}
}

func TestGetBarsRejectsEmptySymbol(t *testing.T) {
	db := connectTestDB(t)
	if _, err := db.GetBars(""); err == nil {
		t.Fatal("expected an error for an empty symbol")
	}
}

func TestPingSucceedsAgainstLiveDatabase(t *testing.T) {
	db := connectTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := db.Ping(ctx); err != nil {
		t.Fatalf("expected Ping to succeed, got %v", err)
	}
}
