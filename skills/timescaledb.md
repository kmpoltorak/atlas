# TimescaleDB

- Only `prices` is an actual hypertable (`create_hypertable('prices', 'ts')`, migrations/001).
  `indicators`, `predictions`, `recommendations`, `backtest_runs`, etc. are plain relational
  tables — `database-final.md`'s original proposal sketched more of them as hypertables, but
  that's not what got built; don't assume a table is a hypertable without checking its migration.
- No compression policy is configured anywhere. If you're about to add one, that's new scope, not
  restoring something that regressed.
- Migrations are hand-written, numbered SQL files (`migrations/NNN_description.sql`) with **no
  real migration tool and no version tracking table** — official Postgres/TimescaleDB images
  auto-run everything in `docker-entrypoint-initdb.d` (mounted from `migrations/`), but only
  against a *fresh, empty* data volume. Against an existing volume, a new migration must be
  applied by hand (`psql -f migrations/0NN_....sql`) — there's nothing that tracks which
  migrations an existing database has already had applied.
- Idempotent upserts rely on a real unique constraint, not application-level dedup:
  `prices_symbol_ts_unique UNIQUE (symbol_id, ts)` (migrations/002) is the `ON CONFLICT` target
  the collector's upsert logic depends on.
- **`pg_restore -c` (clean, in-place restore) is unsafe against this schema** — confirmed by a
  real recovery-drill rehearsal, not theory: `--clean`'s DROP/ALTER sequence conflicts with
  TimescaleDB's internal per-chunk tables (`_timescaledb_internal._hyper_N_chunk`) and silently
  drops `prices`' primary key/unique/foreign-key constraints without erroring loudly — the damage
  only surfaced later as `ON CONFLICT` failures and stale-OID errors. The correct procedure is to
  restore into a **freshly created, empty** database instead — see `deploy/RUNBOOK.md`'s backup/
  restore section for the exact commands and how it was verified (row-order-independent MD5
  checksums across `transactions`/`positions`/`prices`).
- Repository code uses `sqlx` with named/positional parameterized queries throughout
  (`internal/infrastructure/persistence/`) — no string-built SQL, no `SELECT *` (columns are
  always listed explicitly so a schema change can't silently change what a struct scans into).