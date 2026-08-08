# Prometheus

- Metrics live in `internal/observability/metrics.go`, a hand-built `*prometheus.Registry` (not
  the global default registry) exposed at `/metrics`. Current metrics are all counters +
  histograms — no gauges yet: HTTP request count + duration histogram, market-provider request
  count, recommendations-generated count, predictions-generated count, Discord alerts-sent count.
  Add a new metric by extending the `Metrics` struct and registering it in `NewMetrics()`, not by
  reaching for the package-level `promauto` helpers elsewhere.
- Two separate alerting concerns, don't conflate them:
  - **Business alerts** (`internal/app/alerts_loop.go`) — BUY/SELL/REDUCE recommendation changes,
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