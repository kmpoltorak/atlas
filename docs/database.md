# Database

Tables (see `migrations/*.sql` for the authoritative schema — this list is current as of
migrations/001-020):

- `symbols` — ticker registry, upserted on demand by every write path that needs a symbol_id.
  `name`/`exchange`/`asset_class` (migrations/016) hold what the symbol actually is (fetched once,
  rarely); `last_price`/`previous_close` (migrations/016) hold a live quote refreshed every
  alert-poll cycle, with two clocks: `quote_updated_at` is when Atlas fetched it, `quoted_at`
  (migrations/022) when the market last traded at that price — valuation and freshness read the
  latter, since a provider keeps returning a stopped instrument's last price — see the Watchlist/Alerts pages' "Today" column and the
  Dashboard/Portfolio pages' live P&L overlay.
- `prices` — OHLCV bars, a real TimescaleDB hypertable (`create_hypertable`, migrations/001).
- `indicators` — one row per computed indicator value.
- `predictions` — ML output: `score`/`confidence`/`model_version` always populated;
  `correction_probability` populated only when a real trained model produced it (NULL for the
  deterministic stub); `recovery_probability`/`recovery_model_version` (migrations/010) follow
  the identical convention for the independent recovery model; `expected_duration`/
  `expected_strength` stay NULL regardless — no model produces those yet.
- `portfolios` — one row per named portfolio (up to `portfolio.MaxPortfolios`, currently 5); every
  install starts with one, auto-created on first read (see `persistence.TimescaleDB.
  ListPortfolios`) — not a single-row singleton like `watchlists` below. The dashboard's tabs
  switch which one `portfolio.UseCase.ActivePortfolioID` points at (see docs/decision_engine.md
  for how the decision engine deliberately reads across all of them instead).
- `positions` — current position snapshot per symbol, foreign-keyed to a portfolio.
- `transactions` — append-only trade history; `currency`/`price_in_pln` (migrations/009) support
  recording a transaction in PLN/EUR/USD while all internal math stays in PLN. `timestamp` is the
  trade date, `created_at` (migrations/021) when Atlas accepted the row — the tie-break replay
  orders same-day trades by. The id doubles as an idempotency key: recording an id that already
  exists with the same data is a no-op, which is what makes a resubmitted dashboard form safe.
- `watchlists` — a single bootstrap row, same pattern as `portfolios`.
- `watchlist_items` — tracked symbols.
- `recommendations` — append-only decision-engine output; `market_condition` (migrations/007,
  BULL/BEAR/SIDEWAYS) and `ml_model_version` (migrations/012, distinct from `model_version` —
  see docs/decision_engine.md) were both added after the initial schema, and `degraded`,
  `risk_data_available` and `ml_recovery_model_version` by migrations/022 — rows written before
  that hold NULL for all three, since that history never recorded them.
- `backtest_runs` (migrations/005) — one row per backtest run, holding its metrics and ML
  evaluation results.
- `auth_settings` (migrations/008) held the two-factor login secret; dropped by migrations/023
  when login was removed. Backups taken before that still restore, with that table ignored.
- `ml_model_selection` (migrations/011) — which trained model file is active per role
  (correction/recovery), set from the Account dashboard page — see docs/ml.md.
- `schema_migrations` (migrations/013) — which migration versions have actually been applied to
  this database; see the "Migrations" note below.
- `engine_settings` (migrations/015) — a single-row durable override for whether the ML+Portfolio+
  Risk blend is active, set from the Account dashboard page; no row means it's never been toggled
  there, and the composition root falls back to the `ML_ENABLED` env var.
- `backup_log` (migrations/017) — a single row recording when a full-database backup
  (`persistence.TimescaleDB.BackupAll`, the Account page's "Backup" button) was last taken. Purely
  a safety gate for the "Flush all data" button, not user data: `FlushAll` refuses outright unless
  this row exists and is less than five minutes old, so it's deliberately excluded from the
  backup/restore/flush cycle itself (see `internal/infrastructure/persistence/backup.go`) — a
  Flush doesn't erase the fact that a backup was once taken, and a Restore doesn't overwrite it.
- `leader_election` (migrations/018) — a single-row lease naming which process is currently doing
  the singleton work (polling the watchlist, writing recommendations, sending notifications), with
  an expiry and a monotonic `epoch`. The epoch is a fencing token: a write from a process that has
  since been superseded fails rather than landing late (`persistence.requireLeadership`). Runtime
  state, deliberately excluded from backups.
- `alert_outbox` (migrations/018, extended by 019 and 020) — one row per alert awaiting delivery,
  written *before* anything tries to send it, with the set of channels that still owe it
  (`pending_channels`, a comma-separated TEXT column rather than TEXT[] so no new driver
  dependency was needed). Delivery claims a row with `FOR UPDATE SKIP LOCKED` for a bounded lease
  (`claimed_by`/`claim_expires_at`) and a claim generation (`claim_seq`, migrations/020) that
  fences the acknowledgement, so a worker whose claim lapsed mid-send cannot overwrite the state
  of the worker that took over. Runtime state, excluded from backups.
- `alert_cursor` (migrations/019) — the last action actually *announced* per symbol, committed in
  the same transaction as the outbox row that announced it. This is not the same claim as
  `recommendations`, which ordinary HTTP browsing also writes; deriving "have we told the user"
  from that table meant a crash between the two could silently suppress a real alert. Included in
  backups since format version 2.

There is no `alerts` table: the Alerts page and `GET /alerts` show each watched symbol's latest
row in `recommendations`, written by the background poll (`LatestRecommendations`).

**Backup format.** The Account page's backup is a JSON document of every user-data table, tagged
with a format version. Version 1 predates `alert_cursor`, version 2 adds it, version 3 drops
`auth_settings` with login. Every version restores: a table the schema no longer has is ignored,
and a column added since the backup takes its default, so a backup taken before a migration still
restores after it (`persistence.RestoreAll`).

## Implementation status

`indicators` is real and actively written, not merely migrated with nothing writing to it: every
live-recommendation path (`GET /recommendations`, `GET /alerts`, the background alert-poll loop)
persists the full computed indicator set as a side-effect write (migrations/001+006,
`internal/infrastructure/persistence/indicators.go`). Indicators are still computed fresh per
request everywhere — this is a write-only audit trail, not a read/caching path. Backtesting
deliberately doesn't write indicator snapshots, since its bars are historical/simulated, not live
readings (see docs/backtesting.md).

Only `prices` is an actual TimescaleDB hypertable — every other table above is a plain relational
table, despite this doc's original proposal sketching more of them as hypertables. No TimescaleDB
compression or retention *policy* is configured; the "10 years" figure below was a target, never
implemented. Retention that does exist is application-level and covers derived data only:
`PruneDerivedData` drops indicator, recommendation and prediction rows past
`DERIVED_RETENTION_DAYS`, once a day, alongside delivered `alert_outbox` rows. It is **off by
default** — the env var defaults to `0`, meaning keep everything; set it (e.g. 365) on a long-lived
instance. Prices, portfolio state and transactions are never pruned regardless.

Indexes: `symbols.ticker`, `prices(symbol_id, ts)` (a real unique constraint,
`prices_symbol_ts_unique` from migrations/002, is what makes the collector's upsert idempotent),
`backtest_runs(symbol_id, created_at)` (migrations/005). Most other lookups go through a small
number of tables at a scale where an explicit index hasn't been needed yet.

Migrations are hand-written, numbered SQL files, embedded with `go:embed` and applied by the
application itself at startup under a `pg_advisory_lock` — so a fresh volume and an existing one
converge on the same schema without anyone running `psql` by hand. Which versions actually landed
is recorded in `schema_migrations` (migrations/013); every migration from 013 onward inserts its
own row. Every migration must be safe to run twice: Postgres has no `ADD CONSTRAINT IF NOT EXISTS`, so a
constraint migration checks `pg_constraint` inside a `DO $$` block, and a migration that only works
once is broken. Restoring a `pg_dump` with `pg_restore -c` silently drops `prices`' constraints on
this schema — restore into a fresh database instead ([runbook.md](runbook.md)).
