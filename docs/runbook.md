# Runbook

Backup, restore and recovery procedures for the Docker Compose stack described in
[deployment.md](deployment.md).

There are two independent backup paths, and they answer different questions:

- **The Settings page's Backup / Restore** — a JSON document of every user-data table, taken and
  restored by Atlas itself (`persistence.WriteBackup`/`RestoreAll`). It survives schema upgrades:
  a backup taken before a migration still restores after it, taking column defaults for anything
  added since. Use it for "move my data" and "undo a flush".
- **`pg_dump` of the whole database** — below. It includes everything, runtime tables and
  TimescaleDB internals too, and is what to reach for when the database volume itself is lost.

Neither runs on a schedule and neither leaves the host on its own. A backup that exists only on
the machine it protects does not survive losing that machine: copy dumps somewhere else.

## What needs backing up, and where it already lives

| Target | Where it lives | How it recovers |
|---|---|---|
| TimescaleDB (prices, portfolios, transactions, watchlist, recommendations) | `timescaledb-data` volume | A dump or a Settings-page backup — the only thing here with no other copy |
| Configuration | This Git repository (`.env` is gitignored by design) | `git checkout` |
| Grafana dashboards, Prometheus/Alertmanager config | In Git (`grafana/`, `prometheus/`, `alertmanager/`), provisioned automatically | `git checkout` |
| ML models | `training/models/` (gitignored) | Re-run training from TimescaleDB's own price history — reproducible, not instant. Atlas runs without a model (RSI stub) meanwhile |

## TimescaleDB backup

```bash
docker compose exec timescaledb pg_dump -U atlas -d atlas -F c -f /tmp/atlas-backup.dump
docker compose cp timescaledb:/tmp/atlas-backup.dump ./atlas-backup-$(date +%F).dump
```

## TimescaleDB restore

```bash
# Restore into a fresh, empty database — NOT pg_restore -c against the existing "atlas" database.
# See "Why a fresh database, not -c" below for why that matters.
docker compose cp ./atlas-backup-<date>.dump timescaledb:/tmp/restore.dump
docker compose exec timescaledb psql -U atlas -d postgres -c "DROP DATABASE IF EXISTS atlas_restored;"
docker compose exec timescaledb psql -U atlas -d postgres -c "CREATE DATABASE atlas_restored OWNER atlas;"
docker compose exec timescaledb pg_restore -U atlas -d atlas_restored /tmp/restore.dump

# Once verified below, point atlas-api at it (DATABASE_NAME, or rename the databases) and
# restart atlas-api.
```

Verify after restore:

```bash
docker compose exec timescaledb psql -U atlas -d atlas_restored -c "SELECT count(*) FROM symbols;"
curl -s http://localhost:8080/health   # expect {"status":"healthy", ...}
```

### Why a fresh database, not `-c`

This runbook originally documented `pg_restore -U atlas -d atlas -c ./backup.dump` — restoring
in place over the existing database with `--clean`. Rehearsing it for real (see "Recovery drill
results" below) found this **does not work reliably against TimescaleDB hypertables**:
`--clean`'s `DROP TABLE`/`ALTER TABLE` sequence conflicts with TimescaleDB's internal chunk
tables (`_timescaledb_internal._hyper_*_chunk`) — Postgres refused most of the DDL
(`operation not supported on chunk tables`, `ONLY option not supported on hypertable
operations`), and pg_dump itself already warns about this on the way out (`circular
foreign-key constraints on ... continuous_agg`). In this repo's case the existing data
happened to survive because most of it was already there and unchanged — but that's luck, not
a property of the command, and in a genuine disaster (a lost/corrupted database) there's no
"already there and unchanged" data to fall back on.

Restoring into a **freshly created, empty** database sidesteps all of it: the dump already
contains `CREATE EXTENSION IF NOT EXISTS timescaledb` plus every internal chunk table
(`_timescaledb_internal._hyper_*_chunk`) and the extension's own catalog rows, so `pg_restore`
recreates the whole hypertable from scratch in one pass instead of trying to reconcile a
`--clean` DROP/ALTER sequence against chunk tables that already exist. This is also the
realistic disaster scenario anyway — a lost volume means starting from nothing, not patching a
live database in place.

### Recovery drill results (2026-08-07, Docker Compose)

Rehearsed for real against the actual dev database (test data only — no real portfolio data
exists yet, so this was safe to run directly rather than against a throwaway copy):

1. Snapshotted row counts and an order-independent MD5 checksum (`md5(string_agg(md5(row::text),
   ''))`) of `transactions`, `positions`, and `prices` before touching anything.
2. Ran the documented backup command — succeeded, produced a 307K dump (34 symbols, 3925 price
   bars, 26 transactions, 5 positions, 207 indicator rows, 160 recommendations, and more).
3. First tried the *old* `pg_restore -c` command from this runbook against the live `atlas`
   database — failed as described above (65 ignored errors). Data (rows) happened to survive
   unchanged (confirmed via the same checksums), but **schema didn't**: the ignored errors turned
   out not to be harmless after all — `prices`' primary key, unique, and foreign-key constraints
   were left silently missing afterward (`\d prices` showed zero constraints), which only
   surfaced later as real test failures (`ON CONFLICT` upserts need the unique constraint to
   target) and as `atlas-api`'s long-running DB connections erroring on stale cached relation
   OIDs for `symbols` (a table the failed restore also touched, though its own schema stayed
   intact — the OID error was a client-side cache problem, not a symbols-table problem). Fixed by
   manually re-adding exactly the constraints `migrations/001`/`002` define
   (`PRIMARY KEY (id, ts)`, `FOREIGN KEY (symbol_id) REFERENCES symbols(id)`,
   `UNIQUE (symbol_id, ts)`, verified first that no duplicate/orphaned rows existed to block
   re-adding them) and restarting `atlas-api` to drop its stale connections. This is the clearest
   argument in this whole drill for never running `pg_restore -c` against a hypertable-containing
   database again — restoring into a fresh database (step 4) doesn't have this failure mode at all.
4. Restored into a freshly created `atlas_restored` database instead (the corrected procedure
   above) — succeeded with only 2 ignorable errors (a redundant FK constraint attempt against an
   internal chunk table, harmless since TimescaleDB enforces that FK at the hypertable level
   already). Every table's row count matched the original exactly, all three checksums matched
   byte-for-byte, and `prices` came back as a real hypertable
   (`timescaledb_information.hypertables` still lists it), not a degraded plain table.
5. Cleaned up: dropped `atlas_restored`, deleted the dump file (both the in-container `/tmp` copy
   and the host copy) — nothing left behind.

## Recovery priority

1. **Database** — restore the most recent dump or Settings-page backup. Nothing else is useful
   without it.
2. **Configuration** — `git checkout` this repository at the last known-good commit.
3. **Application** — `docker compose up -d`.
4. **ML models** — retrain (`training/retrain.sh`) and pick a candidate on the Settings page.

## Infrastructure alerting

Separate from the Discord alerts `internal/app/alerts_loop.go` already sends for recommendation
changes, `prometheus/alerts.yml` defines Prometheus alerting rules for Atlas's own health:

| Alert | Fires when |
|---|---|
| `AtlasAPIDown` | `/metrics` hasn't been scrapeable for 2 minutes |
| `AtlasAPIFlapping` | atlas-api's up/down state changed more than 4 times in 15 minutes (crash-loop proxy that needs no extra scrape config) |
| `AtlasHighErrorRate` | more than 5% of requests returned a 5xx status over 5 minutes |
| `AtlasAlertPollErrors` | a whole background alert-poll cycle failed at least once in 15 minutes (e.g. the market data provider is unreachable) |

Docker Compose wires this end to end: `prometheus` loads `alerts.yml` and forwards firing alerts
to the `alertmanager` service (`:9093`), which routes them to a Discord channel — deliberately a
*different* one from `DISCORD_WEBHOOK_URL`'s business alerts, so infra noise never mixes with
investment notifications. Set `ALERTMANAGER_DISCORD_WEBHOOK_URL` in your (gitignored) `.env` —
see `.env.example` — to enable real delivery; leave it empty and Alertmanager still starts and
runs fine, it just logs a failed delivery attempt per alert instead of posting anywhere (the same
non-fatal-optional-dependency pattern as everywhere else in this project).

The webhook URL itself is never written to a file tracked by Git: `alertmanager/alertmanager.yml`
points `discord_configs`' `webhook_url_file` at `/tmp/discord_webhook_url`, which
`docker-compose.yml`'s `alertmanager` service writes from that env var at container start (an
overridden entrypoint, since Alertmanager's config can't read env vars directly, only file
paths).

**Pinned image version matters here**: `prom/alertmanager:v0.27.0` (the version this stack
started on) has a real bug — confirmed live and via its own source on GitHub — where
`discord_configs`' config-load validation only checks `WebhookURL`, never `WebhookURLFile`, so
every `webhook_url_file`-only config fails to load at all (`no discord webhook URL provided`),
regardless of the file's actual content. Fixed in `v0.28.0`; this stack is pinned to `v0.28.1`.

Verified live end to end: config loads cleanly both with `ALERTMANAGER_DISCORD_WEBHOOK_URL` empty
and set; posting a synthetic alert to Alertmanager's API against a fake (but real-shaped) webhook
URL produced a genuine round trip to Discord's actual API (`404 Unknown Webhook`), logged as a
failed delivery, Alertmanager kept running — exactly the intended non-fatal degrade.

```bash
docker compose up -d prometheus alertmanager
curl -s http://localhost:9090/api/v1/rules | jq '.data.groups[].rules[].name'
curl -s http://localhost:9093/api/v2/status
```
