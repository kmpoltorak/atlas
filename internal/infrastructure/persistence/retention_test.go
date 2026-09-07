package persistence

import (
	"testing"
	"time"
)

// The 2026-09-07 review's third finding: derivedTables named predictions.created_at, a column
// that does not exist on that table (migrations/001 timestamps predictions with ts). Every prune
// therefore failed with `column "created_at" does not exist`, so with DERIVED_RETENTION_DAYS set
// nothing was pruned — and because the loop aborted on the first error and Go's map iteration
// order is random, an arbitrary subset of the other tables was skipped too.
//
// Run against a real database: the failure was a SQL error, which no fake reproduces.
func TestPruneDerivedDataDeletesOldRowsFromEveryTable(t *testing.T) {
	db := connectTestDB(t)

	symbolID := seedRetentionSymbol(t, db)
	old := time.Now().UTC().Add(-400 * 24 * time.Hour)
	recent := time.Now().UTC().Add(-time.Hour)

	for _, row := range []struct {
		table  string
		column string
		ts     time.Time
	}{
		{"predictions", "ts", old},
		{"predictions", "ts", recent},
		{"indicators", "ts", old},
		{"indicators", "ts", recent},
		{"recommendations", "created_at", old},
		{"recommendations", "created_at", recent},
	} {
		insertRetentionRow(t, db, row.table, row.column, symbolID, row.ts)
	}

	deleted, err := db.PruneDerivedData(365 * 24 * time.Hour)
	if err != nil {
		t.Fatalf("prune must succeed against the real schema: %v", err)
	}
	if deleted < 3 {
		t.Fatalf("expected at least the three seeded old rows to go, got %d", deleted)
	}

	for _, check := range []struct{ table, column string }{
		{"predictions", "ts"},
		{"indicators", "ts"},
		{"recommendations", "created_at"},
	} {
		var remaining int
		if err := db.db.Get(&remaining,
			"SELECT count(*) FROM "+check.table+" WHERE symbol_id = $1 AND "+check.column+" < $2",
			symbolID, time.Now().UTC().Add(-366*24*time.Hour)); err != nil {
			t.Fatalf("count %s: %v", check.table, err)
		}
		if remaining != 0 {
			t.Fatalf("%s still holds %d rows past the retention window", check.table, remaining)
		}
		var kept int
		if err := db.db.Get(&kept,
			"SELECT count(*) FROM "+check.table+" WHERE symbol_id = $1", symbolID); err != nil {
			t.Fatalf("count %s: %v", check.table, err)
		}
		if kept == 0 {
			t.Fatalf("%s lost its recent row too; retention must only remove what is past the window", check.table)
		}
	}
}

func seedRetentionSymbol(t *testing.T, db *TimescaleDB) string {
	t.Helper()
	var id string
	if err := db.db.Get(&id,
		`INSERT INTO symbols (ticker) VALUES ('TESTRET')
		 ON CONFLICT (ticker) DO UPDATE SET ticker = EXCLUDED.ticker
		 RETURNING id`); err != nil {
		t.Fatalf("seed symbol: %v", err)
	}
	t.Cleanup(func() {
		for _, table := range []string{"predictions", "indicators", "recommendations"} {
			if _, err := db.db.Exec("DELETE FROM "+table+" WHERE symbol_id = $1", id); err != nil {
				t.Errorf("clean up %s: %v", table, err)
			}
		}
		if _, err := db.db.Exec("DELETE FROM symbols WHERE id = $1", id); err != nil {
			t.Errorf("clean up symbol: %v", err)
		}
	})
	return id
}

func insertRetentionRow(t *testing.T, db *TimescaleDB, table, column, symbolID string, ts time.Time) {
	t.Helper()
	var query string
	switch table {
	case "predictions":
		query = `INSERT INTO predictions (id, symbol_id, ts, model_version) VALUES (gen_random_uuid(), $1, $2, 'test')`
	case "indicators":
		query = `INSERT INTO indicators (id, symbol_id, ts, indicator_type, value) VALUES (gen_random_uuid(), $1, $2, 'rsi', 50)`
	case "recommendations":
		query = `INSERT INTO recommendations (id, symbol_id, action, created_at) VALUES (gen_random_uuid(), $1, 'HOLD', $2)`
	default:
		t.Fatalf("unknown table %s", table)
	}
	if _, err := db.db.Exec(query, symbolID, ts); err != nil {
		t.Fatalf("insert into %s: %v", table, err)
	}
}
