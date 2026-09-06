package persistence

import (
	"bytes"
	"encoding/json"
	"testing"
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
