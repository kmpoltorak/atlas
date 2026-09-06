package persistence

import (
	"encoding/json"
	"testing"
)

func TestBackupValidationBeforeDatabaseAccess(t *testing.T) {
	invalid := []string{`{}`, `null`, `{"items":[]}`, `[]`, `{"format":"atlas-backup","version":99,"tables":{}}`}
	for _, payload := range invalid {
		// No connection: validation must reject before even beginning a transaction.
		if err := (&TimescaleDB{}).RestoreAll([]byte(payload)); err == nil {
			t.Fatalf("accepted %s", payload)
		}
	}
}

// tablesFor builds an all-empty document for one format version.
func tablesFor(version int) map[string]json.RawMessage {
	tables := map[string]json.RawMessage{}
	for _, table := range tablesForVersion(version) {
		tables[table] = json.RawMessage(`[]`)
	}
	return tables
}

// A backup taken before alert_cursor existed must still restore. Refusing yesterday's file
// because today's build knows one more table is a worse failure than restoring without a
// notification cursor, which only means the next poll re-announces the watchlist.
func TestBackupValidationAcceptsEveryVersionThisBuildSupports(t *testing.T) {
	current := tablesFor(backupVersion)
	legacy := tablesFor(1)

	cases := map[string]any{
		"untagged legacy export": legacy,
		"tagged v1":              backupDocument{Format: "atlas-backup", Version: 1, Tables: legacy},
		"tagged current":         backupDocument{Format: "atlas-backup", Version: backupVersion, Tables: current},
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := validateBackup(raw); err != nil {
				t.Fatalf("%s must validate: %v", name, err)
			}
		})
	}
}

// A version this build does not know about is refused rather than partially applied — the file
// may contain tables whose meaning has changed.
func TestBackupValidationRejectsAFutureVersion(t *testing.T) {
	raw, _ := json.Marshal(backupDocument{
		Format: "atlas-backup", Version: backupVersion + 1, Tables: tablesFor(backupVersion),
	})
	if _, err := validateBackup(raw); err == nil {
		t.Fatal("a newer-format backup must be refused, not restored on a guess")
	}
}

func TestBackupValidationRejectsMalformedTables(t *testing.T) {
	for _, bad := range []string{`null`, `{}`, `[null]`, `[{}]`, `[42]`} {
		tables := tablesFor(backupVersion)
		tables["positions"] = json.RawMessage(bad)
		raw, _ := json.Marshal(backupDocument{Format: "atlas-backup", Version: backupVersion, Tables: tables})
		if _, err := validateBackup(raw); err == nil {
			t.Fatalf("accepted malformed table: %s", bad)
		}
	}

	tables := tablesFor(backupVersion)
	delete(tables, "positions")
	raw, _ := json.Marshal(backupDocument{Format: "atlas-backup", Version: backupVersion, Tables: tables})
	if _, err := validateBackup(raw); err == nil {
		t.Fatal("accepted a document missing a table")
	}
}

// Pending deliveries and the leader lease are runtime state, not user data. Restoring a queue
// would fire alerts about a market state hours stale; restoring a lease would block every live
// replica until it expired.
func TestRuntimeTablesAreNotBackedUp(t *testing.T) {
	for _, table := range runtimeOnlyTables {
		for _, backed := range backupTables {
			if table == backed {
				t.Fatalf("%s is runtime state and must not be in backupTables", table)
			}
		}
	}
}
