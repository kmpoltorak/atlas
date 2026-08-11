package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
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
}

// BackupAll dumps every row of every table in backupTables as JSON, keyed by table name — a
// full, portable snapshot suitable for RestoreAll on this same database or a fresh one (see
// account.html's "Backup" button). row_to_json/json_agg do the type-to-JSON encoding inside
// Postgres itself (timestamps, UUIDs, jsonb, NULLs), so this needs no per-table Go struct that
// could drift from the schema.
func (t *TimescaleDB) BackupAll() ([]byte, error) {
	ctx := context.Background()
	dump := make(map[string]json.RawMessage, len(backupTables))
	for _, table := range backupTables {
		var rows []byte
		query := fmt.Sprintf(`SELECT COALESCE(json_agg(row_to_json(t)), '[]') FROM %s t`, table)
		if err := t.db.QueryRowxContext(ctx, query).Scan(&rows); err != nil {
			return nil, fmt.Errorf("dump %s: %w", table, err)
		}
		dump[table] = rows
	}
	payload, err := json.Marshal(dump)
	if err != nil {
		return nil, fmt.Errorf("encode backup: %w", err)
	}
	if err := t.RecordBackup(); err != nil {
		return nil, fmt.Errorf("record backup: %w", err)
	}
	return payload, nil
}

// RecordBackup durably marks that a full backup was just taken — the safety record FlushAll
// checks before allowing a wipe (see migrations/017). Called automatically at the end of a
// successful BackupAll; a separate method only so FlushAll's check (LastBackupAt) has a single,
// explicit thing to read rather than inferring "was a backup taken" some other way.
func (t *TimescaleDB) RecordBackup() error {
	ctx := context.Background()
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
	ctx := context.Background()
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
func (t *TimescaleDB) RestoreAll(payload []byte) error {
	var dump map[string]json.RawMessage
	if err := json.Unmarshal(payload, &dump); err != nil {
		return fmt.Errorf("decode backup: %w", err)
	}

	ctx := context.Background()
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

// FlushAll deletes every row of every table in backupTables — a full factory reset, back to the
// state a freshly migrated, never-used database would be in. Login itself is unaffected: the
// admin username/password come from ADMIN_USERNAME/ADMIN_PASSWORD_HASH (see config.Load), not
// any of these tables — only 2FA enrollment (auth_settings) is reset, matching a real fresh
// install where 2FA has never been set up. Callers must also reset whatever in-memory state
// mirrors these tables (see portfolio.UseCase.Reload/watchlist.UseCase.Reload) — this only
// touches the database.
//
// Refuses outright (ErrNoBackupTaken) if no backup has ever been recorded — a server-side gate,
// not just a disabled button (see migrations/017's backup_log): a "Flush all data" click can't
// destroy the only copy of the data with nothing to restore from. This checks existence only, not
// recency — the Backup button's own instructions ask for a fresh one right before a risky flush,
// but this can't tell how stale an existing one is.
func (t *TimescaleDB) FlushAll() error {
	if _, ok, err := t.LastBackupAt(); err != nil {
		return fmt.Errorf("check last backup: %w", err)
	} else if !ok {
		return ErrNoBackupTaken
	}

	ctx := context.Background()
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

func truncateAll(ctx context.Context, tx *sqlx.Tx) error {
	if _, err := tx.ExecContext(ctx, "TRUNCATE "+strings.Join(backupTables, ", ")+" CASCADE"); err != nil {
		return fmt.Errorf("truncate: %w", err)
	}
	return nil
}
