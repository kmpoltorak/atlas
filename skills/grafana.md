# Grafana

- One provisioned dashboard exists today: `grafana/dashboards/overview.json`. Provisioning is
  automatic (`grafana/provisioning/{datasources,dashboards}/*.yml`) — dropping a new `.json` file
  into `grafana/dashboards/` is enough, no manual "import" step, no dashboard UUID to register
  anywhere else.
- The datasource is Prometheus, pointed at the `prometheus` service's real query API
  (`grafana/provisioning/datasources/datasource.yml`) — panels query real
  `internal/observability/metrics.go` series, not sample/demo data.
- Default login is `admin`/`admin`; override the password via `GRAFANA_ADMIN_PASSWORD`
  (`.env`/`.env.example`), never hardcode a real password into a committed dashboard JSON or
  provisioning file.
- If you add a new Prometheus metric (`prometheus.md`), it doesn't automatically appear anywhere
  useful — add a panel for it to `overview.json`, or it's invisible in practice even though
  `/metrics` exposes it.