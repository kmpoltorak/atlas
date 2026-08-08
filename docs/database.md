# Database

Tables (see `migrations/*.sql` for the authoritative schema — this list is current as of
migrations/001-012):

- `symbols` — ticker registry, upserted on demand by every write path that needs a symbol_id.
- `prices` — OHLCV bars, a real TimescaleDB hypertable (`create_hypertable`, migrations/001).
- `indicators` — one row per computed indicator value.
- `predictions` — ML output: `score`/`confidence`/`model_version` always populated;
  `correction_probability` populated only when a real trained model produced it (NULL for the
  deterministic stub); `recovery_probability`/`recovery_model_version` (migrations/010) follow
  the identical convention for the independent recovery model; `expected_duration`/
  `expected_strength` stay NULL regardless — no model produces those yet.
- `portfolios` — a single bootstrap row (`name = "default"`), not a real multi-portfolio model —
  see `positions`/`transactions` below.
- `positions` — current position snapshot per symbol, foreign-keyed to the default portfolio.
- `transactions` — append-only trade history; `currency`/`price_in_pln` (migrations/009) support
  recording a transaction in PLN/EUR/USD while all internal math stays in PLN.
- `watchlists` — a single bootstrap row, same pattern as `portfolios`.
- `watchlist_items` — tracked symbols.
- `recommendations` — append-only decision-engine output; `market_condition` (migrations/007,
  BULL/BEAR/SIDEWAYS) and `ml_model_version` (migrations/012, distinct from `model_version` —
  see docs/decision_engine.md) were both added after the initial schema.
- `backtest_runs` (migrations/005) — one row per backtest run, holding its metrics and ML
  evaluation results.
- `auth_settings` (migrations/008) — a single-row 2FA enrollment state (TOTP secret, enabled
  flag) for the one admin account.
- `ml_model_selection` (migrations/011) — which trained model file is active per role
  (correction/recovery), set from the Account dashboard page — see docs/ml.md.

There is no `alerts` table — alerts are computed live from the watchlist on every request, not
persisted (see `internal/application/alerts`).

## Implementation status

`indicators` is real and actively written, not merely migrated with nothing writing to it: every
live-recommendation path (`GET /recommendations`, `GET /alerts`, the background alert-poll loop)
persists the full computed indicator set as a side-effect write (migrations/001+006,
`internal/infrastructure/persistence/indicators.go`). Indicators are still computed fresh per
request everywhere — this is a write-only audit trail, not a read/caching path. Backtesting
deliberately doesn't write indicator snapshots, since its bars are historical/simulated, not live
readings (see docs/backtesting.md).

Only `prices` is an actual TimescaleDB hypertable — every other table above is a plain relational
table, despite this doc's original proposal sketching more of them as hypertables. No compression
policy and no retention policy are configured anywhere; "10 years" below was a target, never
implemented (there's nothing to prune yet at this project's real data volume).

Indexes: `symbols.ticker`, `prices(symbol_id, ts)` (a real unique constraint,
`prices_symbol_ts_unique` from migrations/002, is what makes the collector's upsert idempotent),
`backtest_runs(symbol_id, created_at)` (migrations/005). Most other lookups go through a small
number of tables at a scale where an explicit index hasn't been needed yet.

Migrations are hand-written, numbered SQL files with no real migration tool and no version
tracking table — see `skills/timescaledb.md` for how they actually get applied (fresh volume vs.
existing one), and for the real `pg_restore -c` hypertable-constraint bug this project hit and
fixed (see `deploy/RUNBOOK.md`'s recovery drill).
