package persistence

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// backupTables lists every table BackupAll/RestoreAll/FlushAll operate on, in dependency order —
// each table only ever references one earlier in this list — so RestoreAll's inserts satisfy
// every foreign key without a deferred-constraint dance. schema_migrations is deliberately
// excluded: it's Atlas's own bookkeeping for which migrations have run, not user data, and
// restoring it from an old backup onto a differently-versioned schema would be actively wrong.
//
// These are compile-time constants, never user input, so building queries by interpolating them
// (below) carries none of the injection risk building a query from a request would.
var backupTables = []string{
	"symbols", "portfolios", "watchlists", "watchlist_items", "positions", "transactions",
	"prices", "indicators", "recommendations", "predictions", "backtest_runs",
	"engine_settings", "ml_model_selection", "auth_settings",
	// alert_cursor is included because losing it is not neutral: restored without it, the next
	// poll sees every action as new and re-announces the entire watchlist.
	"alert_cursor",
}

// runtimeOnlyTables are deliberately absent from backupTables. They describe what this cluster is
// doing right now, not what the user owns, and restoring them would be actively wrong:
//
//   - leader_election: restoring a lease would hand ownership to a process that no longer exists,
//     blocking every live replica until it expired.
//   - alert_outbox: a queue of notifications that were pending at backup time. Restoring it into
//     a fresh database would deliver alerts about a market state hours or days stale; restoring
//     into an existing one would leave that database's own queue untouched anyway. Pending
//     deliveries are not user data, and the cursor above is what actually needs to survive.
//
// Their rows are dropped by FlushAll along with everything else, which is correct — a wiped
// instance has no queue and no leader.
var runtimeOnlyTables = []string{"leader_election", "alert_outbox"}

// BackupAll dumps every row of every table in backupTables as JSON, keyed by table name — a
// full, portable snapshot suitable for RestoreAll on this same database or a fresh one (see
// account.html's "Backup" button). row_to_json/json_agg do the type-to-JSON encoding inside
// Postgres itself (timestamps, UUIDs, jsonb, NULLs), so this needs no per-table Go struct that
// could drift from the schema.
func (t *TimescaleDB) BackupAll() ([]byte, error) {
	var buffer bytes.Buffer
	if err := t.WriteBackup(&buffer); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// WriteBackup streams the same document straight to w, one table at a time, so the process never
// holds more than the largest single table at once.
//
// This is the difference between a backup that works and one that OOMs the container: BackupAll
// used to build every table plus the assembled document in memory, roughly three copies of the
// whole database at peak. On a long-lived instance the prices and recommendations tables are the
// bulk of it and they only grow (see PruneDerivedData).
//
// The snapshot is still one REPEATABLE READ transaction, so streaming does not cost consistency:
// every table is read at the same point in time regardless of how long the write takes.
func (t *TimescaleDB) WriteBackup(w io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), backupTimeout)
	defer cancel()
	tx, err := t.db.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin backup snapshot: %w", err)
	}
	defer tx.Rollback()

	buffered := bufio.NewWriter(w)
	if _, err := fmt.Fprintf(buffered, `{"format":%q,"version":%d,"tables":{`,
		backupFormat, backupVersion); err != nil {
		return fmt.Errorf("write backup header: %w", err)
	}
	for i, table := range backupTables {
		separator := ","
		if i == 0 {
			separator = ""
		}
		if _, err := fmt.Fprintf(buffered, `%s%q:`, separator, table); err != nil {
			return fmt.Errorf("write backup table %s: %w", table, err)
		}
		// json_agg builds the array inside Postgres, which is also where the type-to-JSON
		// encoding belongs (timestamps, UUIDs, jsonb, NULLs) — the mirror of RestoreAll's
		// json_populate_recordset. Streaming here is about not holding every table at once, not
		// about avoiding that.
		var rows []byte
		query := fmt.Sprintf(`SELECT COALESCE(json_agg(row_to_json(t)), '[]') FROM %s t`, table)
		if err := tx.QueryRowxContext(ctx, query).Scan(&rows); err != nil {
			return fmt.Errorf("dump %s: %w", table, err)
		}
		if _, err := buffered.Write(rows); err != nil {
			return fmt.Errorf("write backup table %s: %w", table, err)
		}
		rows = nil // release before the next, larger table is read
	}
	if _, err := buffered.WriteString("}}"); err != nil {
		return fmt.Errorf("write backup footer: %w", err)
	}
	if err := buffered.Flush(); err != nil {
		return fmt.Errorf("flush backup: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit backup snapshot: %w", err)
	}
	return nil
}

// backupFormat/backupVersion tag the document so RestoreAll can tell an Atlas backup from any
// other JSON that happens to be an object (see validateBackup).
const (
	backupFormat = "atlas-backup"
	// backupVersion 2 added alert_cursor. Version 1 documents are still restorable — see
	// validateBackup — because refusing yesterday's backup is a worse failure than restoring one
	// without a notification cursor, which simply means the next poll re-announces the watchlist.
	backupVersion = 2
	// backupMinVersion is the oldest document this build can restore.
	backupMinVersion = 1
)

// tablesForVersion is the table set a document of a given version is expected to carry. Adding a
// table to a backup is a format change, and pretending otherwise is how a restore starts silently
// rejecting every file taken before the change.
func tablesForVersion(version int) []string {
	if version < 2 {
		// v1 predates alert_cursor.
		v1 := make([]string, 0, len(backupTables))
		for _, table := range backupTables {
			if table != "alert_cursor" {
				v1 = append(v1, table)
			}
		}
		return v1
	}
	return backupTables
}

// backupTimeout bounds a whole backup or restore. Deliberately far longer than queryTimeout:
// this is one long-running snapshot over every table, not an interactive query.
const backupTimeout = 2 * time.Minute

// RecordBackup durably marks that a full backup was just taken — the safety record FlushAll
// checks before allowing a wipe (see migrations/017). Deliberately NOT called by BackupAll:
// producing the bytes is not the same event as the operator actually holding the file, and the
// FlushAll safety net has to mean the latter. accountBackupHandler calls this only after the
// whole payload has been written to the client without error.
func (t *TimescaleDB) RecordBackup() error {
	ctx, cancel := t.context()
	defer cancel()
	if _, err := t.db.ExecContext(ctx, `
		INSERT INTO backup_log (id, last_backup_at) VALUES (TRUE, NOW())
		ON CONFLICT (id) DO UPDATE SET last_backup_at = EXCLUDED.last_backup_at`); err != nil {
		return fmt.Errorf("record backup: %w", err)
	}
	return nil
}

// LastBackupAt returns when BackupAll was last confirmed taken (see RecordBackup). ok is false
// when no backup has ever been recorded — the normal state for an instance that has never used
// the Backup button, not an error; FlushAll uses this to refuse a wipe with no safety net to
// fall back on.
func (t *TimescaleDB) LastBackupAt() (at time.Time, ok bool, err error) {
	ctx, cancel := t.context()
	defer cancel()
	err = t.db.QueryRowxContext(ctx, `SELECT last_backup_at FROM backup_log WHERE id = TRUE`).Scan(&at)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("get last backup time: %w", err)
	}
	return at, true, nil
}

// RestoreAll replaces every row of every table in backupTables with the contents of a BackupAll
// payload — used both to recover after FlushAll and to seed a fresh environment from an existing
// one's backup. All-or-nothing: runs inside one transaction, so a malformed or partial payload
// leaves existing data untouched instead of half-overwritten. json_populate_recordset defers
// JSON-to-column type coercion to Postgres itself, the mirror image of BackupAll's row_to_json.
type backupDocument struct {
	Format  string                     `json:"format"`
	Version int                        `json:"version"`
	Tables  map[string]json.RawMessage `json:"tables"`
}

// validateBackup also accepts complete legacy exports. Missing/null tables are never
// interpreted as a request to delete data; a deliberately empty table must be [].
func validateBackup(payload []byte) (map[string]json.RawMessage, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, fmt.Errorf("decode backup: %w", err)
	}
	dump := root
	// An untagged document is a legacy export, which predates alert_cursor by definition.
	version := 1
	if _, ok := root["format"]; ok {
		var doc backupDocument
		if err := json.Unmarshal(payload, &doc); err != nil {
			return nil, fmt.Errorf("decode backup document: %w", err)
		}
		if doc.Format != backupFormat {
			return nil, errors.New("not an Atlas backup")
		}
		if doc.Version < backupMinVersion || doc.Version > backupVersion {
			return nil, fmt.Errorf("backup version %d is not supported by this build (supports %d-%d)",
				doc.Version, backupMinVersion, backupVersion)
		}
		version = doc.Version
		dump = doc.Tables
	}
	expected := tablesForVersion(version)
	if len(dump) != len(expected) {
		return nil, fmt.Errorf("backup must contain every table for version %d (%d expected, %d present)",
			version, len(expected), len(dump))
	}
	for _, table := range expected {
		raw := bytes.TrimSpace(dump[table])
		if len(raw) == 0 || raw[0] != '[' {
			return nil, fmt.Errorf("backup table %s must be an array", table)
		}
		var rows []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, fmt.Errorf("invalid backup table %s: %w", table, err)
		}
		for _, row := range rows {
			if len(row) == 0 {
				return nil, fmt.Errorf("invalid empty row in %s", table)
			}
		}
	}
	return dump, nil
}

func (t *TimescaleDB) RestoreAll(payload []byte) error {
	dump, err := validateBackup(payload)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), backupTimeout)
	defer cancel()
	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin restore transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op once Commit has succeeded below

	if err := truncateAll(ctx, tx); err != nil {
		return err
	}
	for _, table := range backupTables {
		rows, ok := dump[table]
		if !ok || string(rows) == "null" {
			continue
		}
		query := fmt.Sprintf(`INSERT INTO %s SELECT * FROM json_populate_recordset(null::%s, $1::json)`, table, table)
		if _, err := tx.ExecContext(ctx, query, string(rows)); err != nil {
			return fmt.Errorf("restore %s: %w", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit restore transaction: %w", err)
	}
	return nil
}

// ErrNoBackupTaken is FlushAll's refusal when no backup has ever been recorded (see
// LastBackupAt) — checked by name so accountFlushHandler can show a specific, actionable message
// instead of a generic failure.
var ErrNoBackupTaken = errors.New("no backup has been taken yet — download one from the Account page first")

// ErrBackupTooOld is FlushAll's refusal when a backup exists but predates FlushBackupFreshness —
// a backup from before recent changes wouldn't actually get you back to where the database is
// right now, so it doesn't count as a safety net for a wipe happening right now.
var ErrBackupTooOld = fmt.Errorf("last backup is more than %s old — download a fresh one first", FlushBackupFreshness)

// FlushBackupFreshness is how recent FlushAll requires LastBackupAt to be. Deliberately a fixed
// wall-clock window, not tied to ALERT_POLL_INTERVAL_SECONDS (a different concern — alert
// staleness, not backup safety): a backup taken 20 minutes ago no longer reflects "right before
// this wipe" once more transactions/watchlist edits have happened since.
const FlushBackupFreshness = 5 * time.Minute

// FlushAll deletes every row of every table in backupTables — a full factory reset, back to the
// state a freshly migrated, never-used database would be in. Login itself is unaffected: the
// admin username/password come from ADMIN_USERNAME/ADMIN_PASSWORD_HASH (see config.Load), not
// any of these tables — only 2FA enrollment (auth_settings) is reset, matching a real fresh
// install where 2FA has never been set up. Callers must also reset whatever in-memory state
// mirrors these tables (see portfolio.UseCase.Reload/watchlist.UseCase.Reload) — this only
// touches the database.
//
// Refuses outright — a server-side gate, not just a disabled button (see migrations/017's
// backup_log) — if no backup has ever been recorded (ErrNoBackupTaken) or the last one is older
// than FlushBackupFreshness (ErrBackupTooOld): a "Flush all data" click can't destroy data with
// no recent copy to restore from.
func (t *TimescaleDB) FlushAll() error {
	at, ok, err := t.LastBackupAt()
	if err != nil {
		return fmt.Errorf("check last backup: %w", err)
	}
	if !ok {
		return ErrNoBackupTaken
	}
	if time.Since(at) > FlushBackupFreshness {
		return ErrBackupTooOld
	}

	ctx, cancel := t.context()
	defer cancel()
	tx, err := t.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin flush transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := truncateAll(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit flush transaction: %w", err)
	}
	return nil
}

// truncateAll clears the user data AND the runtime tables. The runtime ones are not restored (see
// runtimeOnlyTables) but they must still be wiped: a flushed instance with a stale outbox would
// deliver notifications about positions that no longer exist.
func truncateAll(ctx context.Context, tx *sqlx.Tx) error {
	all := append(append([]string{}, backupTables...), runtimeOnlyTables...)
	if _, err := tx.ExecContext(ctx, "TRUNCATE "+strings.Join(all, ", ")+" CASCADE"); err != nil {
		return fmt.Errorf("truncate: %w", err)
	}
	return nil
}
