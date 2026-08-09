# Prometheus

- Metrics live in `internal/observability/metrics.go`, a hand-built `*prometheus.Registry` (not
  the global default registry) exposed at `/metrics`: HTTP request count + duration histogram,
  market-provider request count, recommendations-generated count, predictions-generated count,
  Discord alerts-sent count, background alert-poll error count, and one gauge —
  `atlas_dependency_up{dependency="database|redis|trainer|prometheus|alertmanager|grafana"}`,
  refreshed every 15s by `app.StartDependencyStatusPolling` from the same checks `/health` runs
  (`Application.dependencyStatuses`). Add a new metric by extending the `Metrics` struct and
  registering it in `NewMetrics()`, not by reaching for the package-level `promauto` helpers
  elsewhere.
- `cmd/trainer` is a **second, separate** registry in a separate process (`newTrainerMetrics`,
  port 8091, scraped as the `atlas-trainer` job) — deliberately not `observability.Metrics`,
  which is shaped around atlas-api's counters. It exposes `atlas_trainer_runs_total{outcome}`,
  `atlas_trainer_run_duration_seconds`, `atlas_trainer_last_run_timestamp_seconds`. Its target
  reads as permanently down while the `trainer` Compose profile isn't started; that's expected,
  not a broken scrape.
- Two separate alerting concerns, don't conflate them:
  - **Business alerts** (`internal/app/alerts_loop.go`) — BUY/ACCUMULATE/REDUCE/SELL recommendation changes,
    pushed straight to Discord via `DISCORD_WEBHOOK_URL`. Nothing to do with Prometheus.
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
- Grafana dashboards live in `grafana/dashboards/`, provisioned automatically on container start
  (no manual "import" step) — admin login is `admin`/`GRAFANA_ADMIN_PASSWORD` (default `admin`).