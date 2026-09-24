# Atlas — working rules

Read this before changing anything. These are not style preferences: nearly every rule below
exists because the alternative was tried and cost something measurable. Where a rule says a thing
was verified live, it was.

Scope and hard product constraints: [PROJECT_CONTEXT.md](PROJECT_CONTEXT.md). How the system
works: [docs/](docs/). Why the expensive decisions were made: [docs/adr/](docs/adr/).

[implementation-plan.md](implementation-plan.md) is **not** a status document any more — the
project diverged from the plan it describes. Read `docs/` and the code for what exists; don't
treat a gap between that file and the codebase as a defect, and don't update it as part of a
change.

## Working method

The project documentation is the source of truth. Read it before assuming. Don't invent missing
requirements, don't simplify architecture without asking, and don't swap a technology without
explicit approval. For anything structural: explain the change and what it touches first.

**Never present a number that wasn't measured.** No illustrative metrics, no placeholder
performance figures, no "typically around" — see [docs/adr/005](docs/adr/005-no-fabricated-numbers.md).
A signal measured on one window is a hypothesis, not a finding.

## Go

- Clean Architecture, four layers: `internal/domain` (types + pure business rules, no I/O) →
  `internal/application` (use cases, orchestration) → `internal/infrastructure` (DB/cache/
  provider/notification adapters) → `internal/app` (HTTP handlers + `app.go`'s composition root).
  Business logic never lives in an HTTP handler — handlers parse the request, call a use case,
  shape the response.
- Dependency injection happens once, in `internal/app/app.go`'s `New()` — every use case is
  constructed there and wired with its real infrastructure adapters. No `init()`-time wiring, no
  package-level singletons.
- Every optional external dependency (market provider, ML model, Discord webhook, NBP exchange
  rates, admin auth) is nil-safe and non-fatal when unconfigured — the app always starts and
  degrades gracefully (deterministic stub prediction, no Discord delivery, etc.) rather than
  refusing to boot. Two exceptions are deliberate and both are startup-fatal: TimescaleDB/Redis
  connection failures, and admin credentials left unset without an explicit `AUTH_DISABLED=true`
  — an unauthenticated dashboard has to be asked for, not arrived at by omission
  (`config.Validate`).
- Concurrency-shared state that changes after startup uses `atomic.Pointer[T]`, not a plain
  field guarded by convention — e.g. `ml.UseCase`'s `model`/`recoveryModel` fields, swapped live
  from the Account dashboard's model picker while `Predict()` runs concurrently on other
  requests. A plain pointer field there would be a real data race, not just poor style.
- Batch operations over independent items collect and return `errors.Join(errs...)` rather than
  aborting on the first failure — see `portfolio.UseCase.RemoveTransactions` (bulk-delete) and
  `Application.Close()` (releases DB/Redis/ONNX resources even if one fails).
- Error wrapping: always `fmt.Errorf("doing the thing: %w", err)`, never a bare error string —
  every error in this codebase should be traceable back through `errors.Is`/`errors.As`.
- Logging is `go.uber.org/zap`, structured fields only (`zap.String(...)`, `zap.Error(err)`), no
  `fmt.Println`/`log.Printf` in production code paths (a CLI tool printing its own output, like
  `cmd/generate-admin-credentials`, is a legitimate exception — it's not a server).
- Configuration is plain `os.Getenv` (`internal/config/config.go`) and stays externalized — no
  Viper, and no hardcoded secrets or magic numbers. An early guidelines document listed Viper and
  the Factory/Strategy patterns as the intended stack; none of them were ever built, and what the
  codebase actually uses is constructor functions plus interface-based swapping
  (`market.MarketProvider`, `notifications.Notifier`). Don't "restore" any of them.
- No worker pools and no benchmark tests exist in this codebase today — don't assume either
  pattern is in use elsewhere as precedent.
- The non-negotiables, in one place: no `panic()`, no global mutable state, no `SELECT *` against
  a live table, no ignored errors, and never delete a test to make a change pass. Always take a
  `context.Context`, always wrap errors, always keep interfaces and packages small.

## Finance

- Atlas **never executes trades** — no brokerage integration exists or should be added without
  an explicit, separate design conversation. It is decision support only; this is a hard
  constraint, not a phrasing preference.
- **Never output a bare action.** Every recommendation carries why that action was chosen, which
  indicators contributed, the ML confidence, the portfolio impact and the risk level. An action
  without its explanation is not a shippable output.
- Six real actions, not five: `BUY`, `ACCUMULATE`, `HOLD`, `REDUCE`, `SELL`, `WAIT`
  (`internal/domain/decision`). `ACCUMULATE`/`REDUCE` vs `BUY`/`SELL` are gated on whether a real
  portfolio position already exists, not just score severity — see `docs/decision_engine.md`.
- The score is **not** a weighted sum. Technical is the only opportunity term; ML, portfolio P&L
  and portfolio-wide risk are multiplicative factors that can only lower it
  (`internal/application/decision`'s `Orchestrator.blendMLAndPortfolio`, each defaulting to
  "no penalty" rather than "neutral" when its signal never ran). It used to be a 0.50/0.25/0.15/
  0.10 average, and that shape is what let a term carrying no information — an empty portfolio's
  risk score, pinned at its maximum — read as good news and lift every symbol's score. Don't
  restore the weights; `docs/decision_engine.md` has the current design.
- **A ledger is ordered by two clocks, not one.** `Timestamp` is when the trade happened and may
  be backdated; `RecordedAt` (`transactions.created_at`, migrations/021) is when Atlas accepted
  it, and it is what breaks ties — the dashboard form submits a backdated trade as a date with no
  time, so same-day entries are genuinely equal on the first. Ordering ties by transaction id ordered them by a
  random UUID, which reordered a BUY and the SELL it funded and rejected the pair as an oversell.
  Replay (`portfolio.UseCase.recomputePosition`) is the single path both recording and deleting
  go through; keep it that way.
- Every recommendation persists with a real `Reason []string` explanation, confidence score, and
  `RiskScore`/`MarketCondition` fields — never just an action label. `GET /recommendations`'s
  response and the `recommendations` table (migrations/001/007) both carry the full explanation,
  not just the verdict.
- Confidence and score are two different things: `Confidence` reflects how far the ML prediction
  sits from 0.5 (uncertain) toward 0/1 (certain); `Score` is the blended decision value that maps
  to an action band. Don't conflate "the model is confident" with "the recommendation is
  positive" — a confident SELL and a confident BUY both have high `Confidence`.
- Market condition (`BULL`/`BEAR`/`SIDEWAYS`) is a deterministic ADX+EMA technical rule
  (`ml.ClassifyMarketCondition`), not an ML prediction — don't describe it as "the model
  predicts the market is bullish," it's a rule, not a trained output.

## ML

- **Python trains, Go infers, and the production image depends on neither Python nor the Go
  toolchain.** Python is for dataset preparation, feature engineering, experiments, training,
  validation and hyperparameter search — nothing else. Loading models, predicting, scoring
  confidence and exposing ML metrics all happen inside the Go application. Do not add a Python
  backend service, and do not move inference out of Go. `cmd/trainer` is the one exception and
  proves the rule: it needs the Go toolchain and a virtualenv at runtime, which is exactly why it
  lives in its own container (`training/Dockerfile`) and never in `atlas-api`'s image.
- **A prediction is a signal, not a decision.** The decision engine owns the recommendation.
  Every prediction carries its model version, confidence, timestamp and input context; every
  model carries validation metrics, a version, and a reproducible training process.
- Two independent, optional binary classifiers exist: correction-probability (`MODEL_PATH`) and
  recovery-probability (`RECOVERY_MODEL_PATH`) — same XGBoost→ONNX pipeline, same Go inference
  mechanics (`ml.OnnxModel.PredictCorrectionProbability` is reused for both roles; only the label
  they were trained against differs). Either, both, or neither can be loaded; nothing else in the
  codebase assumes one implies the other.
- `internal/application/ml.FeatureOrder` (Go) and `training/train.py`'s `FEATURE_ORDER` (Python)
  **must stay in the same order** — `rsi, ema_dist, macd_pct, tr_pct, atr_pct, rvol_20, rvol_50, volume` today. The ONNX
  model has no column names at inference time, only positional floats; `ml.LoadOnnxModel` refuses
  to load a model whose metadata sidecar's `feature_order` doesn't match, so a mismatch fails
  loudly at load time instead of silently mis-scoring — don't remove that check to "just make it
  load."
- **Every feature must be scale-free** — a ratio or a bounded index, never a raw price or share
  count. This is the one hard rule in this section. The original set (`ema`, `macd`, `atr` in
  price units) let a single feature encode the symbol's price level, and since per-symbol
  correction base rates run from 0.00 to 0.58, the model scored by memorising symbol identity: a
  predictor using no features at all beat it, 0.757 AUC to 0.707. If you add a feature, normalise
  it by close (or bound it) in `ml.BuildFeatureVector`, and rename it if you changed an existing
  one's meaning — the `feature_order` guard compares names, so reusing a name silently loads stale
  models against new semantics.
- **The exporter and the evaluator must be given the same label parameters, not just call the
  same function.** `cmd/export-training-data` builds `CorrectionLabelParams` from the full
  `DefaultCorrectionLabelParams()`, because leaving `VolatilityMultiple` at its zero value is
  what silently reverts the barrier to a fixed percentage — the exporter and
  `backtesting.evaluateML` then agreed on every function call and disagreed on the arguments, so
  the model was trained on one event and scored against another. Every export writes a
  `<csv>.params.json` sidecar and `training/train.py` copies it into the model's metadata, so a
  model states the barrier its labels actually came from rather than the one the defaults are
  supposed to be.
- `cmd/export-training-data` reuses the exact same Go feature-computation code
  (`ml.BuildFeatureVector`) the live inference path uses — normalisation included, which is why
  it lives there and not in the exporter. Never reimplement indicator math in Python; that's
  exactly the drift this design avoids.
- `training/train.py` computes a **symbol-base-rate baseline** ("predict each symbol's historical
  positive rate, ignore all features") on every run and warns loudly when the model loses to it.
  Don't delete that check to make a run look clean — it exists because a model losing to it once
  shipped as production-looking numbers.
- The verdict is judged on **PR AUC, not ROC AUC**, and that is load-bearing rather than pedantic.
  Atlas alerts on the top of the ranking; ROC AUC is dominated by the uneventful middle no alert
  touches. They disagree in practice: the shipped correction model trails the baseline on ROC AUC
  (0.710 vs 0.748) while beating it on PR AUC (0.264 vs 0.188), and at its 25 highest-scoring bars
  it was right 68% of the time against a 6.5% base rate. Judging on ROC AUC would condemn a model
  that does the job it is deployed to do.
- **Volatility is the dominant feature family for this target** — the largest single win in the
  project's history. `atr_pct` alone took prototype ROC AUC from 0.675 to 0.782; adding
  `rvol_20`/`rvol_50` roughly doubled PR AUC. Note `analyzer.ComputeATR` returns raw per-bar true
  range despite its name; `ComputeSmoothedATR` is the real ATR and the one the model uses. Longer
  windows (100+ bars) were tried and lose — most symbols hold only ~125 bars.
- Don't add `scale_pos_weight` back. Measured on this dataset it bought ~0.01 ROC AUC and
  tripled the Brier score (0.211 vs 0.067). The output is consumed as a probability, not a
  ranking — the decision engine blends `1 - correction_probability` — so calibration wins.
- Every trained model ships with a `.json` metadata sidecar next to its `.onnx` file (version,
  training row count, holdout ROC AUC, PR AUC, Brier score, accuracy, the symbol-base-rate
  baseline and whether the model beat it, label distribution, label definition,
  `feature_order`) — required, not optional documentation. `ml.ListModels` (the Account
  dashboard's model picker) reads it to build each option's description from real numbers, never
  a fabricated or hand-edited one.
- Recovery labels are conditional: `ml.ComputeRecoveryLabel` only produces a label for bars where
  a correction actually happened (`ComputeCorrectionLabel` returned 1) — recovery isn't a
  meaningful question otherwise. This makes the recovery training set structurally much smaller
  than the correction set. Note that "smaller" turned out not to mean "worse": once the features
  were made scale-free, recovery was the model that beat its baseline and correction was the one
  that didn't, reversing this project's earlier assumption. Judge each against its own baseline
  rather than by dataset size.
- Model deployment is never fully automatic. `training/retrain.sh` can generate a new candidate
  on a schedule, but it never changes what's live — a human picks a candidate from the Account
  dashboard page after reviewing its real holdout metrics (`ml.UseCase.SetModel`/
  `SetRecoveryModel`, hot-swapped via `atomic.Pointer`, no restart needed, durably recorded in
  `ml_model_selection` — migrations/011). This is deliberate: training data here is real but
  still thin and skewed, and this project's stance is "never auto-promote an unreviewed model
  into live recommendations" — the same rule already applied to walk-forward threshold
  optimization (`docs/backtesting.md`).
- When no model is loaded, `Predict()` falls back to a deterministic RSI-derived stub
  (`ml.StubModelVersion`) for correction, and leaves `recovery_probability` at `0` for recovery —
  a normal, logged, non-fatal startup state, not an error. Persistence follows a
  NULL-means-not-computed convention: a stub's correction probability is never written as a
  literal value, since `0`/`0.5` would misrepresent "not computed" as a real prediction.
- **The correction label is volatility-scaled, not a fixed percentage** — the barrier is 4x the
  instrument's own trailing 20-bar realised volatility, floored at 2% (`CorrectionLabelParams`).
  A fixed 5% asked a different question of every instrument (GLD never qualified, SOXL qualified
  in 58% of windows), which is what made the per-symbol base-rate baseline so hard to beat. The
  threshold is computed strictly from bars at or before the feature bar — don't "optimise" it
  into a per-symbol precomputation, that leaks forward information into the label.
- **Judge a model economically, not only statistically.** `StrategyResult.MLContribution` runs
  the backtest twice, with and without the ML term, and reports the return/Sharpe/drawdown delta.
  AUC cannot tell you whether acting on a model beats ignoring it. On the first run this revealed
  that the model improves aggregate returns while *worsening* Sharpe and drawdown — see
  docs/backtesting.md.
- Recovery probability now reaches the decision: the ML blend term is
  `1 - P(correction) * (1 - P(recovery))`, i.e. one minus the probability of an unrecovered drop
  (`decision.mlScoreFromPrediction`). It degrades to the old `1 - P(correction)` when no recovery
  model is loaded.

## TimescaleDB

- Only `prices` is an actual hypertable (`create_hypertable('prices', 'ts')`, migrations/001).
  `indicators`, `predictions`, `recommendations`, `backtest_runs`, etc. are plain relational
  tables — the original design proposal sketched more of them as hypertables, but that's not what
  got built; don't assume a table is a hypertable without checking its migration.
- No compression policy is configured anywhere. If you're about to add one, that's new scope, not
  restoring something that regressed.
- Migrations are hand-written, numbered SQL files (`migrations/NNN_description.sql`), embedded via
  `go:embed` and applied by the application itself at startup under a `pg_advisory_lock`. Every
  migration from 013 onward ends with its own
  `INSERT INTO schema_migrations (version) VALUES ('0NN') ON CONFLICT (version) DO NOTHING;`, so
  `SELECT version FROM schema_migrations ORDER BY version;` always reflects reality. Postgres has
  no `ADD CONSTRAINT IF NOT EXISTS`, so a constraint migration needs a `DO $$` block checking
  `pg_constraint` — a migration that only works once is a broken migration.
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

## Docker

- Multi-stage build (`Dockerfile`): a `golang` builder stage compiles the binary, then it's
  copied into a slim runtime stage — no Go toolchain in the shipped image. The builder needs
  `COPY migrations ./migrations`: they are embedded with `go:embed`, so the build fails without
  them.
- Runtime base is `debian:bookworm-slim`, **not Alpine**. Deliberate, not an oversight:
  `onnxruntime_go` dlopens a native `libonnxruntime.so` at runtime (the repo's only cgo
  dependency), built against glibc — musl (Alpine) can't load it reliably. Don't "optimize" this
  back to Alpine without re-solving that.
- Non-root user must be a **numeric UID**, not a named user: `USER 10001` after
  `useradd --uid 10001 appuser`. Kubernetes' `runAsNonRoot` pod security check can't resolve
  `/etc/passwd` before the container starts, so a named `USER appuser` fails
  `CreateContainerConfigError` on real Kubernetes even though it works under Docker Compose
  (which doesn't enforce `runAsNonRoot` at all) — this broke for real during a recovery-drill
  rehearsal on minikube; see `deploy/RUNBOOK.md`'s Recovery drill section. The same lesson applies
  to any other image without a non-root default user (e.g. the backup CronJob's
  `timescale/timescaledb` image needed an explicit `runAsUser`/`fsGroup` for the same reason).
- Healthcheck hits the real `/health` endpoint, not a process-exists check — same in
  `docker-compose.yml` and the Kubernetes deployment's liveness probe. atlas-api uses
  `curl -fsS http://localhost:8080/health`, not `wget`: its runtime base is `debian:bookworm-slim`
  (see above), which doesn't ship `wget` — a real bug once, confirmed live via `docker inspect`
  showing a nonstop "unhealthy" status despite `/health` genuinely returning 200 (fixed by adding
  `curl` to the Dockerfile's package list). atlas-trainer's own healthcheck still uses
  `wget -qO- http://localhost:8091/health`, which is fine there: its image is the full
  `golang:1.26.8-bookworm` (not `-slim`), which does ship `wget`.
- Volumes: `timescaledb-data`/`redis-data`/`prometheus-data`/`grafana-data`/`alertmanager-data`
  are named/persistent, plus `trainer-gomod-cache` holding atlas-trainer's Go module cache (that
  service runs via `go run`, so without it every container recreation re-downloads the same
  dependencies). `./training/models:/models:ro` is a read-only bind mount so
  `MODEL_PATH`/`RECOVERY_MODEL_PATH` (or the Account dashboard's model picker) can reference a
  locally-trained model without rebuilding the image; atlas-trainer mounts the whole repo
  read-write instead, since `retrain.sh` writes into `training/data`/`training/models`.
- Graceful shutdown: `cmd/api/main.go` handles `SIGTERM`/`SIGINT` and calls `Application.Close()`
  (releases DB/Redis/ONNX sessions) before exiting.
- Real secrets never live in `docker-compose.yml` — they're `${VAR:-default}` references reading
  from a gitignored `.env` (see `.env.example` for the full documented list). Docker Compose is
  the primary deployment target (see `docs/deployment.md`); Kubernetes manifests in `deploy/`
  are real and tested against minikube, but production-cluster testing is explicitly out of
  scope for this project.

## Prometheus and Grafana

- Metrics live in `internal/observability/metrics.go`, a hand-built `*prometheus.Registry` (not
  the global default registry) exposed at `/metrics`: HTTP request count + duration histogram,
  market-provider request count, recommendations-generated count, predictions-generated count,
  alerts-sent count, background alert-poll error count, and one gauge —
  `atlas_dependency_up{dependency="database|redis|trainer|prometheus|alertmanager|grafana"}`,
  refreshed every 15s by `app.StartDependencyStatusPolling` from the same checks `/health` runs
  (`Application.dependencyStatuses`). Add a new metric by extending the `Metrics` struct and
  registering it in `NewMetrics()`, not by reaching for the package-level `promauto` helpers
  elsewhere.
- `cmd/trainer` is a **second, separate** registry in a separate process (`newTrainerMetrics`,
  port 8091, scraped as the `atlas-trainer` job) — deliberately not `observability.Metrics`,
  which is shaped around atlas-api's counters. It exposes `atlas_trainer_runs_total{outcome}`,
  `atlas_trainer_run_duration_seconds`, `atlas_trainer_last_run_timestamp_seconds`. It runs in the
  default Compose stack; its target reads as down only where there is no trainer (Kubernetes,
  or a stack started without it), which is expected, not a broken scrape.
- Two separate alerting concerns, don't conflate them:
  - **Business alerts** (`internal/app/alerts_loop.go`) — BUY/ACCUMULATE/REDUCE/SELL
    recommendation changes, delivered through the alert outbox to Discord/Slack/Telegram. Nothing
    to do with Prometheus.
  - **Infrastructure alerts** (`prometheus/alerts.yml`) — `AtlasAPIDown`, `AtlasAPIFlapping`,
    `AtlasPodCrashLooping`, `AtlasHighErrorRate`, evaluated by Prometheus and routed through
    Alertmanager (`alertmanager/alertmanager.yml`) to a **separate** Discord webhook
    (`ALERTMANAGER_DISCORD_WEBHOOK_URL`) — deliberately a different channel so infra noise never
    mixes with investment notifications.
  - `AtlasPodCrashLooping` is inert without `kube-state-metrics` scraping, which this repo
    doesn't configure (cluster-level add-ons are out of scope) — don't treat it as broken if it
    never fires.
- Real upstream gotcha: Alertmanager `v0.27.0`'s config loader only validates
  `discord_configs[].webhook_url`, never `webhook_url_file` — every file-based webhook config
  (which is what this repo uses, to keep the URL out of Git) silently failed to load regardless
  of the file's actual content. Fixed in `v0.28.0`; this stack pins `prom/alertmanager:v0.28.1`.
  Don't downgrade that image tag without knowing this.
- One provisioned Grafana dashboard exists: `grafana/dashboards/overview.json`. Provisioning is
  automatic (`grafana/provisioning/{datasources,dashboards}/*.yml`) — dropping a new `.json` into
  `grafana/dashboards/` is enough, no manual import, no UUID to register. The datasource is the
  real Prometheus query API, so panels show real series, not demo data. Default login is
  `admin`/`admin`, overridden by `GRAFANA_ADMIN_PASSWORD`; never hardcode a real password into a
  committed dashboard JSON or provisioning file. A new metric doesn't appear anywhere useful on
  its own — add a panel for it, or it's invisible in practice even though `/metrics` exposes it.
