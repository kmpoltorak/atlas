package persistence

import (
	"strings"
	"testing"
	"time"

	"atlas/internal/domain/market"
)

func TestTimescaleDBBuildsConnectionString(t *testing.T) {
	db := NewTimescaleDB("localhost", "5432", "atlas", "atlas", "secret")
	dsn := db.DSN()
	if !strings.Contains(dsn, "host=localhost") {
		t.Fatalf("expected host in DSN, got %q", dsn)
	}
	if !strings.Contains(dsn, "dbname=atlas") {
		t.Fatalf("expected db name in DSN, got %q", dsn)
	}
}

func TestTimescaleDBSaveBarsRoundTrips(t *testing.T) {
	db := NewTimescaleDB("localhost", "5432", "atlas", "atlas", "secret")
	bars := []market.Bar{{Symbol: "AAPL", Close: 100, Timestamp: time.Now()}}
	if err := db.SaveBars("AAPL", bars); err != nil {
		t.Fatalf("expected SaveBars to succeed, got %v", err)
	}
	stored, err := db.GetBars("AAPL")
	if err != nil {
		t.Fatalf("expected GetBars to succeed, got %v", err)
	}
	if len(stored) != 1 {
		t.Fatalf("expected one stored bar, got %d", len(stored))
	}
	if stored[0].Close != 100 {
		t.Fatalf("expected stored close to be 100, got %v", stored[0].Close)
	}
}
