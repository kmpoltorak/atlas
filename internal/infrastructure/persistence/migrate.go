package persistence

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"
)

// migrationLockID is the key for the session-level Postgres advisory lock the runner holds while
// it applies migrations. Arbitrary but fixed: two API pods starting at once must serialize here,
// or both would try to ALTER the same table. Nothing else in Atlas takes an advisory lock, so
// there is no ordering to deadlock against.
const migrationLockID = 8080_1

// migrationTimeout bounds the whole run, lock wait included. Long enough for an index build on a
// real history table, short enough that a pod stuck behind a lock fails its startup probe with a
// clear error instead of hanging forever.
const migrationTimeout = 5 * time.Minute

// Migrate applies every embedded migration the database has not recorded yet, in filename order,
// each in its own transaction. It replaces the previous arrangement where migrations/ was mounted
// into docker-entrypoint-initdb.d and therefore only ever ran against a fresh volume — an
// existing deployment silently kept an old schema and failed later, at query time.
//
// Safe to call on every start and from more than one process: an advisory lock serializes the
// runs, and schema_migrations makes each file apply at most once.
func Migrate(ctx context.Context, t *TimescaleDB, files fs.FS) error {
	ctx, cancel := context.WithTimeout(ctx, migrationTimeout)
	defer cancel()

	// A session-level lock on a dedicated connection: pg_advisory_lock is held by the session,
	// so it must not be taken on a pooled connection that gets handed to someone else.
	conn, err := t.db.Connx(ctx)
	if err != nil {
		return fmt.Errorf("open migration connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockID)

	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	var recorded []string
	if err := conn.SelectContext(ctx, &recorded, `SELECT version FROM schema_migrations`); err != nil {
		return fmt.Errorf("read applied migrations: %w", err)
	}
	applied := make(map[string]bool, len(recorded))
	for _, version := range recorded {
		applied[version] = true
	}

	names, err := migrationNames(files)
	if err != nil {
		return err
	}
	for _, name := range names {
		version := migrationVersion(name)
		if applied[version] {
			continue
		}
		body, err := fs.ReadFile(files, name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := conn.BeginTxx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		// Belt and braces: every file is supposed to record itself (see migrations/013), but a
		// file that forgot to would otherwise be re-applied on every single start.
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1) ON CONFLICT (version) DO NOTHING`,
			version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}

	// A version recorded in the database with no matching file means this binary is older than
	// the schema it is talking to — the case that used to surface as a confusing runtime error
	// on some unrelated query.
	known := make(map[string]bool, len(names))
	for _, name := range names {
		known[migrationVersion(name)] = true
	}
	var unknown []string
	for _, version := range recorded {
		if !known[version] {
			unknown = append(unknown, version)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf("database has migrations this binary does not know about (%s); deploy a newer build",
			strings.Join(unknown, ", "))
	}
	return nil
}

// migrationNames lists the .sql files in filename order, which is also apply order — the 0NN
// prefix makes lexical and numeric order the same thing.
func migrationNames(files fs.FS) ([]string, error) {
	entries, err := fs.Glob(files, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no migrations found")
	}
	sort.Strings(entries)
	return entries, nil
}

// migrationVersion extracts the 0NN prefix that schema_migrations stores, so a migration renamed
// for readability still counts as the same one.
func migrationVersion(name string) string {
	version, _, found := strings.Cut(name, "_")
	if !found {
		return strings.TrimSuffix(name, ".sql")
	}
	return version
}
