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
// Postgres/TimescaleDB service container yet — see .github/workflows/ci.yml).
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
	return db
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
