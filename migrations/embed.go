// Package migrations embeds the SQL files in this directory so the API binary can apply them
// itself at startup (see persistence.Migrate), instead of relying on Postgres's
// docker-entrypoint-initdb.d hook, which only ever runs against a brand-new data volume.
package migrations

import "embed"

// Files holds every migration, named 0NN_description.sql and applied in filename order. Each
// file must be idempotent (IF NOT EXISTS / ADD COLUMN IF NOT EXISTS) and must end with its own
// INSERT INTO schema_migrations, so a database bootstrapped by initdb and one migrated by the
// runner end up recording the same thing.
//
//go:embed *.sql
var Files embed.FS
