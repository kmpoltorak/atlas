package persistence

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"atlas/migrations"
)

// Apply order is filename order, and the version recorded is the 0NN prefix — the two facts the
// runner relies on to skip work it has already done.
func TestMigrationNamesAreOrderedAndVersioned(t *testing.T) {
	names, err := migrationNames(migrations.Files)
	if err != nil {
		t.Fatalf("listing embedded migrations: %v", err)
	}
	if len(names) < 17 {
		t.Fatalf("expected the full migration set to be embedded, got %d files", len(names))
	}
	seen := map[string]bool{}
	previous := ""
	for _, name := range names {
		if name <= previous {
			t.Fatalf("migrations are not in ascending filename order: %q after %q", name, previous)
		}
		previous = name
		version := migrationVersion(name)
		if len(version) != 3 {
			t.Fatalf("migration %q does not start with a 0NN version prefix", name)
		}
		if seen[version] {
			t.Fatalf("two migrations share version %q", version)
		}
		seen[version] = true
	}
}

// Every file must record its own version (the migrations/013 convention) — the runner inserts it
// too, but a file that relies only on that would apply differently through initdb, which has no
// runner behind it.
func TestEveryMigrationRecordsItsOwnVersion(t *testing.T) {
	names, err := migrationNames(migrations.Files)
	if err != nil {
		t.Fatalf("listing embedded migrations: %v", err)
	}
	for _, name := range names {
		body, err := migrations.Files.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		version := migrationVersion(name)
		if version < "013" {
			continue // predates the convention; migrations/013 backfills these
		}
		want := "('" + version + "')"
		if !strings.Contains(string(body), "INSERT INTO schema_migrations") || !strings.Contains(string(body), want) {
			t.Errorf("%s does not record its own version %s in schema_migrations", name, want)
		}
	}
}

func TestMigrationNamesRejectsAnEmptySet(t *testing.T) {
	if _, err := migrationNames(fstest.MapFS{}); err == nil {
		t.Fatal("expected an empty migration set to be an error, not a silent no-op")
	}
}

// Running the real runner twice against a real database must be a no-op the second time, and
// must leave the schema usable — the property that makes it safe on every pod start.
func TestMigrateIsIdempotent(t *testing.T) {
	db := connectTestDB(t)
	ctx := context.Background()

	if err := Migrate(ctx, db, migrations.Files); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	var firstCount int
	if err := db.db.GetContext(ctx, &firstCount, `SELECT count(*) FROM schema_migrations`); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if err := Migrate(ctx, db, migrations.Files); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	var secondCount int
	if err := db.db.GetContext(ctx, &secondCount, `SELECT count(*) FROM schema_migrations`); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if firstCount != secondCount {
		t.Fatalf("second run changed the recorded migration count: %d then %d", firstCount, secondCount)
	}
}
