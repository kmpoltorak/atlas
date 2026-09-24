package persistence

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"atlas/internal/domain/portfolio"
)

// The claim a backup makes is that restoring it gets you back to where you were. Nothing tested
// that end to end — validation was tested, and the dump was tested, but not the round trip, which
// is the only part a user actually depends on.
//
// Destructive by nature (RestoreAll truncates), so it runs only against the disposable test
// database, and it restores the pre-existing snapshot afterwards either way.
func TestBackupRestoreRoundTrip(t *testing.T) {
	db := connectTestDB(t)

	before, err := db.BackupAll()
	if err != nil {
		t.Fatalf("initial backup: %v", err)
	}
	t.Cleanup(func() {
		if err := db.RestoreAll(before); err != nil {
			t.Errorf("failed to restore the original database contents: %v", err)
		}
	})

	portfolioID := testPortfolioID(t, db)
	want := []portfolio.Position{{Symbol: "TESTRT", Quantity: 7, AverageCost: 11, CurrentPrice: 13}}
	if err := db.SavePositions(portfolioID, want); err != nil {
		t.Fatalf("seed positions: %v", err)
	}
	if _, err := db.SaveTransaction(portfolioID, portfolio.Transaction{
		Symbol: "TESTRT", Type: "BUY", Quantity: 7, Price: 11, PriceInPLN: 11,
		Timestamp: "2026-09-05T09:00:00Z",
	}); err != nil {
		t.Fatalf("seed transaction: %v", err)
	}

	var snapshot bytes.Buffer
	if err := db.WriteBackup(&snapshot); err != nil {
		t.Fatalf("backup: %v", err)
	}

	// Wipe the seeded rows, then prove the backup puts them back.
	if err := db.DeletePosition(portfolioID, "TESTRT"); err != nil {
		t.Fatalf("delete seeded position: %v", err)
	}
	if positions, err := db.GetPositions(portfolioID); err != nil {
		t.Fatalf("read positions: %v", err)
	} else if findPosition(positions, "TESTRT") != nil {
		t.Fatal("the seeded position should be gone before the restore")
	}

	if err := db.RestoreAll(snapshot.Bytes()); err != nil {
		t.Fatalf("restore: %v", err)
	}

	positions, err := db.GetPositions(portfolioID)
	if err != nil {
		t.Fatalf("read positions after restore: %v", err)
	}
	restored := findPosition(positions, "TESTRT")
	if restored == nil {
		t.Fatal("the restore did not bring the position back")
	}
	if restored.Quantity != 7 || restored.AverageCost != 11 {
		t.Fatalf("the restored position does not match what was backed up: %+v", *restored)
	}

	transactions, err := db.GetTransactions(portfolioID)
	if err != nil {
		t.Fatalf("read transactions after restore: %v", err)
	}
	found := false
	for _, tx := range transactions {
		if tx.Symbol == "TESTRT" && tx.Quantity == 7 && tx.Price == 11 {
			found = true
		}
	}
	if !found {
		t.Fatal("the restore did not bring the transaction back")
	}
}

// A rejected backup must leave the database exactly as it was — the review's specific worry was
// that validation ran after the TRUNCATE.
func TestRejectedBackupLeavesTheDatabaseUntouched(t *testing.T) {
	db := connectTestDB(t)
	portfolioID := testPortfolioID(t, db)

	if err := db.SavePositions(portfolioID, []portfolio.Position{
		{Symbol: "TESTREJ", Quantity: 3, AverageCost: 5, CurrentPrice: 5},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { _ = db.DeletePosition(portfolioID, "TESTREJ") })

	for name, payload := range map[string]string{
		"empty object":        `{}`,
		"null":                `null`,
		"foreign document":    `{"something":"else"}`,
		"wrong format tag":    `{"format":"not-atlas","version":1,"tables":{}}`,
		"unsupported version": `{"format":"atlas-backup","version":99,"tables":{}}`,
		"table not an array":  `{"symbols":{},"portfolios":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := db.RestoreAll([]byte(payload)); err == nil {
				t.Fatalf("%s was accepted as a backup", name)
			}
			positions, err := db.GetPositions(portfolioID)
			if err != nil {
				t.Fatalf("read positions: %v", err)
			}
			if findPosition(positions, "TESTREJ") == nil {
				t.Fatalf("%s was rejected but the data was destroyed anyway", name)
			}
		})
	}
}

func findPosition(positions []portfolio.Position, symbol string) *portfolio.Position {
	for i := range positions {
		if positions[i].Symbol == symbol {
			return &positions[i]
		}
	}
	return nil
}

// stripColumn removes one column from every row of one table in a backup document — what a
// backup taken before that column's migration looks like.
func stripColumn(t *testing.T, payload []byte, table, column string) []byte {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("decode backup: %v", err)
	}
	var tables map[string][]map[string]json.RawMessage
	if err := json.Unmarshal(doc["tables"], &tables); err != nil {
		t.Fatalf("decode tables: %v", err)
	}
	for _, row := range tables[table] {
		delete(row, column)
	}
	encoded, err := json.Marshal(tables)
	if err != nil {
		t.Fatalf("encode tables: %v", err)
	}
	doc["tables"] = encoded
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode backup: %v", err)
	}
	return out
}

// A backup taken before migrations/021 has no transactions.created_at. Restoring one used to fail
// with SQLSTATE 23502, because the missing key became an explicit NULL that skipped the column's
// DEFAULT — the one backup you need after an upgrade was the one that could not be restored.
func TestBackupFromAnOlderSchemaRestores(t *testing.T) {
	db := connectTestDB(t)

	before, err := db.BackupAll()
	if err != nil {
		t.Fatalf("initial backup: %v", err)
	}
	t.Cleanup(func() {
		if err := db.RestoreAll(before); err != nil {
			t.Errorf("failed to restore the original database contents: %v", err)
		}
	})

	portfolioID := testPortfolioID(t, db)
	if _, err := db.SaveTransaction(portfolioID, portfolio.Transaction{
		Symbol: "TESTOLD", Type: "BUY", Quantity: 2, Price: 9, PriceInPLN: 9,
		Timestamp: "2026-09-01T09:00:00Z",
	}); err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
	current, err := db.BackupAll()
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	legacy := stripColumn(t, current, "transactions", "created_at")

	if err := db.RestoreAll(legacy); err != nil {
		t.Fatalf("a backup from before migrations/021 must restore: %v", err)
	}
	transactions, err := db.GetTransactions(portfolioID)
	if err != nil {
		t.Fatalf("read transactions: %v", err)
	}
	for _, tx := range transactions {
		if tx.Symbol == "TESTOLD" {
			if tx.RecordedAt == "" {
				t.Fatal("the restored transaction should take created_at's default, not stay empty")
			}
			return
		}
	}
	t.Fatal("the restore did not bring the transaction back")
}

// A column the schema requires and cannot default is named, and nothing is touched.
func TestBackupMissingARequiredColumnIsRefusedByName(t *testing.T) {
	db := connectTestDB(t)
	portfolioID := testPortfolioID(t, db)
	if _, err := db.SaveTransaction(portfolioID, portfolio.Transaction{
		Symbol: "TESTREQ", Type: "BUY", Quantity: 1, Price: 3, PriceInPLN: 3,
		Timestamp: "2026-09-01T09:00:00Z",
	}); err != nil {
		t.Fatalf("seed transaction: %v", err)
	}
	current, err := db.BackupAll()
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	err = db.RestoreAll(stripColumn(t, current, "symbols", "ticker"))
	if err == nil || !strings.Contains(err.Error(), "ticker") {
		t.Fatalf("expected a refusal naming the missing ticker column, got %v", err)
	}
	transactions, err := db.GetTransactions(portfolioID)
	if err != nil {
		t.Fatalf("read transactions: %v", err)
	}
	for _, tx := range transactions {
		if tx.Symbol == "TESTREQ" {
			return
		}
	}
	t.Fatal("a refused restore destroyed data")
}
