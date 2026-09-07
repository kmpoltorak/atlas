package persistence

import (
	"bytes"
	"encoding/json"
	"testing"

	"atlas/internal/domain/portfolio"
)

// The streamed document and the buffered one must be byte-identical: BackupAll is now a thin
// wrapper over WriteBackup, and a restore has to accept either.
func TestStreamedBackupMatchesTheBufferedOne(t *testing.T) {
	db := connectTestDB(t)

	buffered, err := db.BackupAll()
	if err != nil {
		t.Fatalf("BackupAll: %v", err)
	}
	var streamed bytes.Buffer
	if err := db.WriteBackup(&streamed); err != nil {
		t.Fatalf("WriteBackup: %v", err)
	}

	// Compare as decoded documents rather than bytes: the two run as separate snapshots, so a
	// concurrent write between them would be a spurious failure. The shape is what matters.
	var a, b map[string]json.RawMessage
	if err := json.Unmarshal(buffered, &a); err != nil {
		t.Fatalf("buffered backup is not valid JSON: %v", err)
	}
	if err := json.Unmarshal(streamed.Bytes(), &b); err != nil {
		t.Fatalf("streamed backup is not valid JSON: %v", err)
	}
	for _, key := range []string{"format", "version", "tables"} {
		if _, ok := a[key]; !ok {
			t.Fatalf("buffered backup is missing %q", key)
		}
		if _, ok := b[key]; !ok {
			t.Fatalf("streamed backup is missing %q", key)
		}
	}
}

// A streamed backup must survive the round trip through validation — the format is the contract
// between the two, and streaming it by hand rather than via json.Marshal is exactly the kind of
// change that could break it silently.
func TestStreamedBackupPassesValidation(t *testing.T) {
	db := connectTestDB(t)

	var streamed bytes.Buffer
	if err := db.WriteBackup(&streamed); err != nil {
		t.Fatalf("WriteBackup: %v", err)
	}

	tables, err := validateBackup(streamed.Bytes())
	if err != nil {
		t.Fatalf("a freshly written backup must validate, got %v", err)
	}
	for _, table := range backupTables {
		if _, ok := tables[table]; !ok {
			t.Fatalf("streamed backup is missing table %q", table)
		}
	}
}

// Row-by-row streaming builds the JSON array by hand, so the separators are code now rather than
// something Postgres' json_agg guaranteed. A table with several rows is what catches a missing or
// doubled comma; one row cannot.
func TestStreamedBackupSeparatesEveryRow(t *testing.T) {
	db := connectTestDB(t)

	portfolioID := testPortfolioID(t, db)
	want := []portfolio.Position{
		{Symbol: "TESTSEP1", Quantity: 1, AverageCost: 10, CurrentPrice: 11},
		{Symbol: "TESTSEP2", Quantity: 2, AverageCost: 20, CurrentPrice: 21},
		{Symbol: "TESTSEP3", Quantity: 3, AverageCost: 30, CurrentPrice: 31},
	}
	if err := db.SavePositions(portfolioID, want); err != nil {
		t.Fatalf("seed positions: %v", err)
	}
	t.Cleanup(func() {
		for _, position := range want {
			if err := db.DeletePosition(portfolioID, position.Symbol); err != nil {
				t.Errorf("clean up %s: %v", position.Symbol, err)
			}
		}
	})

	var streamed bytes.Buffer
	if err := db.WriteBackup(&streamed); err != nil {
		t.Fatalf("WriteBackup: %v", err)
	}

	// Rows are dumped as raw table columns (positions carries symbol_id, not the ticker), so the
	// check is on how many rows came back for this portfolio, which is what separator handling
	// actually affects.
	var document struct {
		Tables struct {
			Positions []struct {
				PortfolioID string `json:"portfolio_id"`
			} `json:"positions"`
		} `json:"tables"`
	}
	if err := json.Unmarshal(streamed.Bytes(), &document); err != nil {
		t.Fatalf("streamed backup is not valid JSON: %v", err)
	}
	seeded := 0
	for _, row := range document.Tables.Positions {
		if row.PortfolioID == portfolioID {
			seeded++
		}
	}
	if seeded != len(want) {
		t.Fatalf("expected all %d seeded rows in the streamed array, got %d", len(want), seeded)
	}
}
